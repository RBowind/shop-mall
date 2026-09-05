package order

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	domorder "shop-mall/backend/internal/order"
	"shop-mall/backend/internal/platform/audit"
	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
)

// ErrOrderNotFound and ErrOrderStateConflict are the order domain's lifecycle
// sentinels, re-exported for the application-layer vocabulary. ErrOrderNotFound
// doubles as the ownership-masking error: a buyer id-scoped query that matches
// nothing is indistinguishable from a row that belongs to another buyer.
var (
	ErrOrderNotFound      = domorder.ErrOrderNotFound
	ErrOrderStateConflict = domorder.ErrOrderStateConflict
)

// RoleResolver resolves the current role of an administrator for the audit
// row. The admin service satisfies it structurally.
type RoleResolver interface {
	FindByID(ctx context.Context, adminID uid.ID) (admin.AdminView, error)
}

// FulfillmentUsecase owns the post-payment order state transitions: the
// administrator ship transition and the buyer confirm-receipt transition. Each
// transition is a single conditional state change guarded by the current
// status; neither involves financial or inventory side effects. The order row
// is locked FOR UPDATE first so concurrent transition attempts serialize: the
// loser sees the already-moved order and gets ErrOrderStateConflict.
type FulfillmentUsecase struct {
	db     *gorm.DB
	orders *domorder.Repository
	roles  RoleResolver
	logger *slog.Logger
	now    func() time.Time
}

// FulfillmentUsecaseDeps is the dependency bundle for NewFulfillmentUsecase.
type FulfillmentUsecaseDeps struct {
	DB     *gorm.DB
	Orders *domorder.Repository
	Roles  RoleResolver
	Logger *slog.Logger
	Now    func() time.Time
}

// NewFulfillmentUsecase validates the dependency bundle and returns the
// usecase.
func NewFulfillmentUsecase(deps FulfillmentUsecaseDeps) (*FulfillmentUsecase, error) {
	if deps.DB == nil {
		return nil, errors.New("order usecase: database is required")
	}
	if deps.Orders == nil {
		return nil, errors.New("order usecase: order repository is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &FulfillmentUsecase{db: deps.DB, orders: deps.Orders, roles: deps.Roles, logger: logger, now: now}, nil
}

// Ship flips a paid order to shipped and records the shipping administrator
// and the ship time. Only paid orders transition to shipped (the OpenAPI
// contract's explicit rule). A missing order returns ErrOrderNotFound; any
// other state returns ErrOrderStateConflict.
func (u *FulfillmentUsecase) Ship(ctx context.Context, adminID, orderID uid.ID) (Order, error) {
	if u == nil || u.db == nil {
		return Order{}, errors.New("order usecase is not configured")
	}
	if uid.IsZero(adminID) || uid.IsZero(orderID) {
		return Order{}, ErrOrderNotFound
	}
	roleName, err := u.resolveRole(ctx, adminID)
	if err != nil {
		return Order{}, err
	}
	var result Order
	err = database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		row, err := u.orders.LockForUpdate(tx, ctx, orderID)
		if err != nil {
			return err
		}
		if row.Status != domorder.StatusPaid {
			return ErrOrderStateConflict
		}
		now := u.now().UTC()
		if err := u.orders.UpdateFields(tx, ctx,
			map[string]any{"id": row.ID},
			map[string]any{
				"status":     domorder.StatusShipped,
				"shipped_by": adminID,
				"shipped_at": now,
			},
		); err != nil {
			return err
		}
		row.Status = domorder.StatusShipped
		row.ShippedByID = &adminID
		row.ShippedAt = &now
		if err := audit.Write(tx, audit.Entry{
			ActorAdminID: &adminID,
			ActorRole:    roleName,
			Action:       "order.ship",
			TargetType:   "order",
			TargetID:     &row.ID,
			Result:       "success",
			AfterData: map[string]any{
				"status":     domorder.StatusShipped,
				"order_no":   row.OrderNo,
				"shipped_at": now,
			},
		}); err != nil {
			return err
		}
		result = ToOrder(row)
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrOrderStateConflict) {
			u.recordFailure(ctx, adminID, roleName, orderID, "order.ship", err)
		}
		return Order{}, err
	}
	return result, nil
}

// Confirm flips a shipped order to completed and stamps completed_at. The
// order must be owned by the buyer; another buyer's order is indistinguishable
// from a missing one (ErrOrderNotFound). Any status other than shipped returns
// ErrOrderStateConflict.
func (u *FulfillmentUsecase) Confirm(ctx context.Context, userID, orderID uid.ID) (Order, error) {
	if u == nil || u.db == nil {
		return Order{}, errors.New("order usecase is not configured")
	}
	if uid.IsZero(userID) || uid.IsZero(orderID) {
		return Order{}, ErrOrderNotFound
	}
	var result Order
	err := database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		row, err := u.orders.LockForUpdateByUser(tx, ctx, userID, orderID)
		if err != nil {
			return err
		}
		if row.Status != domorder.StatusShipped {
			return ErrOrderStateConflict
		}
		now := u.now().UTC()
		if err := u.orders.UpdateFields(tx, ctx,
			map[string]any{"id": row.ID, "user_id": userID},
			map[string]any{
				"status":       domorder.StatusCompleted,
				"completed_at": now,
			},
		); err != nil {
			return err
		}
		row.Status = domorder.StatusCompleted
		row.CompletedAt = &now
		result = ToOrder(row)
		return nil
	})
	if err != nil {
		return Order{}, err
	}
	return result, nil
}

func (u *FulfillmentUsecase) resolveRole(ctx context.Context, adminID uid.ID) (string, error) {
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

func (u *FulfillmentUsecase) recordFailure(ctx context.Context, adminID uid.ID, roleName string, orderID uid.ID, action string, reason error) {
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

func sameOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
