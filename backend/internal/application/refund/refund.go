// Package refund hosts the RefundUsecase, the financial inverse of order
// payment. The buyer request, the administrator approval and the administrator
// rejection all commit their state change in ONE database transaction. On
// approval the transaction restores the points balance (via a NEW order_refund
// ledger entry appended to the append-only ledger), restores the product stock
// by increment, flips the order status to refunded and writes the audit row.
// The transaction runner retries only complete transactions on PostgreSQL
// deadlock (40P01) and serialization failure (40001); a single SQL statement is
// never retried.
package refund

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	apporder "shop-mall/backend/internal/application/order"
	domorder "shop-mall/backend/internal/order"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/audit"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/product"

	"gorm.io/gorm"
)

// ErrInvalidRefundReason is the order domain's request-format sentinel
// (blank or longer than 255 characters), re-exported here; the handler maps
// it to 400.
var ErrInvalidRefundReason = domorder.ErrInvalidRefundReason

// RoleResolver resolves the current role of an administrator for the audit
// row. The admin service satisfies it structurally.
type RoleResolver interface {
	FindByID(ctx context.Context, adminID uid.ID) (admin.AdminView, error)
}

// RefundUsecase owns the refund state machine: the buyer request, the
// administrator approval and the administrator rejection. Request and Confirm
// are the only buyer transitions; the ownership mask is a buyer id-scoped
// query whose no-match is indistinguishable from a missing order.
type RefundUsecase struct {
	db       *gorm.DB
	orders   *domorder.Repository
	products *product.Repository
	ledger   *payment.Repository
	roles    RoleResolver
	logger   *slog.Logger
	now      func() time.Time
}

// RefundUsecaseDeps is the dependency bundle for NewRefundUsecase.
type RefundUsecaseDeps struct {
	DB       *gorm.DB
	Orders   *domorder.Repository
	Products *product.Repository
	Ledger   *payment.Repository
	Roles    RoleResolver
	Logger   *slog.Logger
	Now      func() time.Time
}

// NewRefundUsecase validates the dependency bundle and returns the usecase.
func NewRefundUsecase(deps RefundUsecaseDeps) (*RefundUsecase, error) {
	if deps.DB == nil {
		return nil, errors.New("refund usecase: database is required")
	}
	if deps.Orders == nil {
		return nil, errors.New("refund usecase: order repository is required")
	}
	if deps.Products == nil {
		return nil, errors.New("refund usecase: product repository is required")
	}
	if deps.Ledger == nil {
		return nil, errors.New("refund usecase: ledger repository is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &RefundUsecase{
		db:       deps.DB,
		orders:   deps.Orders,
		products: deps.Products,
		ledger:   deps.Ledger,
		roles:    deps.Roles,
		logger:   logger,
		now:      now,
	}, nil
}

// Request moves a buyer-owned paid or shipped order into refund_requested and
// persists the reason. A fresh request clears the previous review outcome so a
// rejected-then-re-requested order starts clean. The order row is locked first
// so concurrent requests serialize; a wrong state surfaces as
// ErrOrderStateConflict and another buyer's order as ErrOrderNotFound.
func (u *RefundUsecase) Request(ctx context.Context, userID, orderID uid.ID, reason string) (apporder.Order, error) {
	if u == nil || u.db == nil {
		return apporder.Order{}, errors.New("refund usecase is not configured")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 255 {
		return apporder.Order{}, ErrInvalidRefundReason
	}
	if uid.IsZero(userID) || uid.IsZero(orderID) {
		return apporder.Order{}, apporder.ErrOrderNotFound
	}
	var result apporder.Order
	err := database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		row, err := u.orders.LockForUpdateByUser(tx, ctx, userID, orderID)
		if err != nil {
			return err
		}
		switch row.Status {
		case domorder.StatusPaid, domorder.StatusShipped:
		default:
			return apporder.ErrOrderStateConflict
		}
		if err := u.orders.UpdateFields(tx, ctx,
			map[string]any{"id": row.ID, "user_id": userID},
			map[string]any{
				"status":               domorder.StatusRefundRequested,
				"refund_reason":        reason,
				"refund_reject_reason": "",
				"refund_reviewed_by":   gorm.Expr("NULL"),
				"refund_reviewed_at":   gorm.Expr("NULL"),
			},
		); err != nil {
			return err
		}
		row.Status = domorder.StatusRefundRequested
		row.RefundReason = reason
		row.RefundRejectReason = ""
		row.RefundReviewedByID = nil
		row.RefundReviewedAt = nil
		result = apporder.ToOrder(row)
		return nil
	})
	if err != nil {
		return apporder.Order{}, err
	}
	return result, nil
}

// Approve restores the points, the stock and the order state in one
// transaction. The locks are acquired in the documented order: order row, then
// user row, then product rows ordered by product_id ascending. The points are
// restored through a NEW order_refund ledger entry whose balance_after comes
// from the returned balance; the uq_order_refund partial index is the backstop
// that rejects a second refund for the same order. Because every approver
// blocks on the order-row lock and re-reads the committed status, exactly one
// approval performs the side effects; the losers surface as
// ErrOrderStateConflict.
func (u *RefundUsecase) Approve(ctx context.Context, adminID, orderID uid.ID) (apporder.Order, error) {
	if u == nil || u.db == nil {
		return apporder.Order{}, errors.New("refund usecase is not configured")
	}
	if uid.IsZero(adminID) || uid.IsZero(orderID) {
		return apporder.Order{}, apporder.ErrOrderNotFound
	}
	roleName, err := u.resolveRole(ctx, adminID)
	if err != nil {
		return apporder.Order{}, err
	}
	var result apporder.Order
	err = database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		// 1. Lock the order row with its items.
		row, err := u.orders.LockForUpdate(tx, ctx, orderID)
		if err != nil {
			return err
		}
		if row.Status != domorder.StatusRefundRequested {
			return apporder.ErrOrderStateConflict
		}
		// 2. Lock the user row.
		if err := u.ledger.LockUser(tx, ctx, row.UserID); err != nil {
			return translatePaymentError(err)
		}
		// 3. Lock the product rows in ascending product_id order.
		if _, err := u.products.LockByIDs(tx, ctx, productIDsOf(row.Items)); err != nil {
			return err
		}
		now := u.now().UTC()

		// Restore the points; the returned balance feeds balance_after.
		newBalance, err := u.ledger.AdjustBalance(tx, ctx, row.UserID, row.TotalPoints)
		if err != nil {
			return translatePaymentError(err)
		}
		// Append the unique order_refund ledger entry in the same transaction.
		ledgerRow := database.PointsLedger{
			UserID:           row.UserID,
			OrderID:          &row.ID,
			EventKey:         payment.OrderRefundEventKey(row.ID),
			Type:             database.LedgerTypeOrderRefund,
			Delta:            row.TotalPoints,
			BalanceAfter:     newBalance,
			CreatedByAdminID: &adminID,
			Remark:           row.OrderNo,
			CreatedAt:        now,
		}
		if err := u.ledger.Create(tx, ctx, &ledgerRow); err != nil {
			return err
		}
		// Restore the stock: increment, never clobber.
		var restoredUnits int32
		for _, item := range row.Items {
			if _, err := u.products.IncrementStock(tx, ctx, item.ProductID, item.Quantity); err != nil {
				return err
			}
			restoredUnits += item.Quantity
		}
		// Flip the status and stamp the refund timestamps.
		if err := u.orders.UpdateFields(tx, ctx,
			map[string]any{"id": row.ID, "status": domorder.StatusRefundRequested},
			map[string]any{
				"status":             domorder.StatusRefunded,
				"refunded_at":        now,
				"refund_reviewed_by": adminID,
				"refund_reviewed_at": now,
			},
		); err != nil {
			return err
		}
		row.Status = domorder.StatusRefunded
		row.RefundedAt = &now
		row.RefundReviewedByID = &adminID
		row.RefundReviewedAt = &now
		// The audit row in the same transaction.
		if err := audit.Write(tx, audit.Entry{
			ActorAdminID: &adminID,
			ActorRole:    roleName,
			Action:       "refund.approve",
			TargetType:   "order",
			TargetID:     &row.ID,
			Result:       "success",
			AfterData: map[string]any{
				"status":         domorder.StatusRefunded,
				"order_no":       row.OrderNo,
				"total_points":   row.TotalPoints,
				"balance_after":  newBalance,
				"stock_restored": restoredUnits,
				"reviewed_at":    now,
			},
		}); err != nil {
			return err
		}
		result = apporder.ToOrder(row)
		return nil
	})
	if err != nil {
		if errors.Is(err, apporder.ErrOrderStateConflict) {
			u.recordFailure(ctx, adminID, roleName, orderID, "refund.approve", err)
		}
		return apporder.Order{}, err
	}
	return result, nil
}

// Reject refuses a refund request WITHOUT financial or stock side effects. It
// restores the state the request came from (paid for a paid-backed request,
// shipped for a shipped-backed one) and persists the reviewer and rejection
// reason. The shipped-fields consistency CHECK keeps the shipped_by/shipped_at
// pair untouched, so the target state is derived from whether the request was
// shipped-backed.
func (u *RefundUsecase) Reject(ctx context.Context, adminID, orderID uid.ID, reason string) (apporder.Order, error) {
	if u == nil || u.db == nil {
		return apporder.Order{}, errors.New("refund usecase is not configured")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 255 {
		return apporder.Order{}, ErrInvalidRefundReason
	}
	if uid.IsZero(adminID) || uid.IsZero(orderID) {
		return apporder.Order{}, apporder.ErrOrderNotFound
	}
	roleName, err := u.resolveRole(ctx, adminID)
	if err != nil {
		return apporder.Order{}, err
	}
	var result apporder.Order
	err = database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		row, err := u.orders.LockForUpdate(tx, ctx, orderID)
		if err != nil {
			return err
		}
		if row.Status != domorder.StatusRefundRequested {
			return apporder.ErrOrderStateConflict
		}
		target := domorder.StatusPaid
		if row.ShippedByID != nil {
			target = domorder.StatusShipped
		}
		now := u.now().UTC()
		if err := u.orders.UpdateFields(tx, ctx,
			map[string]any{"id": row.ID, "status": domorder.StatusRefundRequested},
			map[string]any{
				"status":               target,
				"refund_reject_reason": reason,
				"refund_reviewed_by":   adminID,
				"refund_reviewed_at":   now,
			},
		); err != nil {
			return err
		}
		row.Status = target
		row.RefundRejectReason = reason
		row.RefundReviewedByID = &adminID
		row.RefundReviewedAt = &now
		if err := audit.Write(tx, audit.Entry{
			ActorAdminID: &adminID,
			ActorRole:    roleName,
			Action:       "refund.reject",
			TargetType:   "order",
			TargetID:     &row.ID,
			Result:       "success",
			AfterData: map[string]any{
				"status":               target,
				"order_no":             row.OrderNo,
				"refund_reject_reason": reason,
				"reviewed_at":          now,
			},
		}); err != nil {
			return err
		}
		result = apporder.ToOrder(row)
		return nil
	})
	if err != nil {
		if errors.Is(err, apporder.ErrOrderStateConflict) {
			u.recordFailure(ctx, adminID, roleName, orderID, "refund.reject", err)
		}
		return apporder.Order{}, err
	}
	return result, nil
}

func (u *RefundUsecase) resolveRole(ctx context.Context, adminID uid.ID) (string, error) {
	if u.roles == nil {
		return "admin", nil
	}
	view, err := u.roles.FindByID(ctx, adminID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(view.RoleName) == "" {
		return "admin", nil
	}
	return view.RoleName, nil
}

// recordFailure writes a best-effort failure audit row for a rejected approval
// or rejection. It runs in its own transaction because the main transaction
// already rolled back.
func (u *RefundUsecase) recordFailure(ctx context.Context, adminID uid.ID, roleName string, orderID uid.ID, action string, reason error) {
	err := database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		return audit.Write(tx, audit.Entry{
			ActorAdminID: &adminID,
			ActorRole:    sameOr(roleName, "admin"),
			Action:       action,
			TargetType:   "order",
			TargetID:     &orderID,
			Result:       "failure",
			AfterData: map[string]any{
				"reason": reason.Error(),
			},
		})
	})
	if err != nil {
		u.logger.ErrorContext(ctx, "record "+action+" failure audit", "error", err)
	}
}

// translatePaymentError maps the payment domain's balance sentinel onto the
// order domain's so the refund surface keeps a single error vocabulary.
func translatePaymentError(err error) error {
	if errors.Is(err, payment.ErrUserNotFound) {
		return apporder.ErrUserNotFound
	}
	return err
}

func productIDsOf(items []database.OrderItem) []uid.ID {
	ids := make([]uid.ID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductID)
	}
	return ids
}

func sameOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
