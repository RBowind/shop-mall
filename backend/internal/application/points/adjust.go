// Package points hosts AdjustPointsUsecase, the administrator-managed points
// adjustment. The ledger entry, the balance update and the audit row commit in
// one transaction; the Idempotency-Key is part of the server-generated event
// key so a retried request maps to the same event and cannot double-apply.
package points

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/audit"
	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
)

// Sentinels the HTTP layer maps to status codes.
var (
	// ErrInvalidIdempotencyKey is a request-format violation (blank or longer
	// than 64 characters). The handler maps it to 400.
	ErrInvalidIdempotencyKey = errors.New("invalid idempotency key")
	// ErrInvalidAdjustment is a business-rule violation (non-positive user id,
	// zero delta, blank remark). The handler maps it to 422.
	ErrInvalidAdjustment = errors.New("invalid points adjustment")
	// ErrUserNotFound is returned when the target buyer row does not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrInsufficientPoints is returned when a negative delta cannot be applied
	// without driving the balance below zero.
	ErrInsufficientPoints = errors.New("insufficient points")
	// ErrIdempotencyConflict is returned when the same Idempotency-Key is
	// replayed with a different request hash. The handler maps it to 409.
	ErrIdempotencyConflict = errors.New("idempotency key replay conflict")
)

// PointsAdjustment is the validated admin adjustment payload.
type PointsAdjustment struct {
	UserID uid.ID
	Delta  int64
	Remark string
}

// RoleResolver resolves the current role of an administrator for the audit
// row. The admin service satisfies it structurally.
type RoleResolver interface {
	FindByID(ctx context.Context, adminID uid.ID) (admin.AdminView, error)
}

// AdjustPointsUsecase executes administrator points adjustments.
type AdjustPointsUsecase struct {
	db     *gorm.DB
	ledger *payment.Repository
	roles  RoleResolver
	logger *slog.Logger
	now    func() time.Time
}

// AdjustPointsUsecaseDeps is the dependency bundle for NewAdjustPointsUsecase.
type AdjustPointsUsecaseDeps struct {
	DB     *gorm.DB
	Ledger *payment.Repository
	Roles  RoleResolver
	Logger *slog.Logger
	Now    func() time.Time
}

// NewAdjustPointsUsecase validates the dependency bundle and returns the
// usecase.
func NewAdjustPointsUsecase(deps AdjustPointsUsecaseDeps) (*AdjustPointsUsecase, error) {
	if deps.DB == nil {
		return nil, errors.New("points usecase: database is required")
	}
	if deps.Ledger == nil {
		return nil, errors.New("points usecase: ledger repository is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &AdjustPointsUsecase{
		db:     deps.DB,
		ledger: deps.Ledger,
		roles:  deps.Roles,
		logger: logger,
		now:    now,
	}, nil
}

// Execute applies the adjustment. The event key embeds the idempotency key, so
// a replay of the same key returns the original ledger entry; a replay with a
// different request hash is ErrIdempotencyConflict. The user row is locked
// before the conditional balance update, the ledger entry and the audit row
// commit in the same transaction.
func (u *AdjustPointsUsecase) Execute(ctx context.Context, adminID uid.ID, idempotencyKey string, req PointsAdjustment) (payment.LedgerEntry, error) {
	if u == nil || u.db == nil {
		return payment.LedgerEntry{}, errors.New("points usecase is not configured")
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return payment.LedgerEntry{}, err
	}
	if err := validateAdjustment(req); err != nil {
		return payment.LedgerEntry{}, err
	}
	if uid.IsZero(adminID) {
		return payment.LedgerEntry{}, ErrInvalidAdjustment
	}
	remark := strings.TrimSpace(req.Remark)
	requestHash := computeAdjustHash(req)
	eventKey := payment.AdminAdjustEventKey(adminID, idempotencyKey)
	roleName, err := u.resolveRole(ctx, adminID)
	if err != nil {
		return payment.LedgerEntry{}, err
	}

	var result payment.LedgerEntry
	err = database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		existing, err := u.ledger.LockByEventKey(tx, ctx, eventKey)
		if err != nil && !errors.Is(err, payment.ErrLedgerEntryNotFound) {
			return err
		}
		if existing != nil {
			if !sameHash(existing.RequestHash, requestHash) {
				return ErrIdempotencyConflict
			}
			result = payment.ToLedgerEntry(existing)
			return nil
		}
		if err := u.ledger.LockUser(tx, ctx, req.UserID); err != nil {
			return translatePaymentError(err)
		}
		newBalance, err := u.ledger.AdjustBalance(tx, ctx, req.UserID, req.Delta)
		if err != nil {
			return translatePaymentError(err)
		}
		row := database.PointsLedger{
			UserID:           req.UserID,
			EventKey:         eventKey,
			RequestHash:      &requestHash,
			Type:             database.LedgerTypeAdminAdjust,
			Delta:            req.Delta,
			BalanceAfter:     newBalance,
			CreatedByAdminID: &adminID,
			Remark:           remark,
			CreatedAt:        u.now().UTC(),
		}
		if err := u.ledger.Create(tx, ctx, &row); err != nil {
			return err
		}
		if err := audit.Write(tx, audit.Entry{
			ActorAdminID: &adminID,
			ActorRole:    roleName,
			Action:       "points.adjust",
			TargetType:   "user",
			TargetID:     &req.UserID,
			Result:       "success",
			AfterData: map[string]any{
				"user_id":       req.UserID,
				"delta":         req.Delta,
				"balance_after": newBalance,
				"remark":        remark,
			},
		}); err != nil {
			return err
		}
		result = payment.ToLedgerEntry(&row)
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInsufficientPoints), errors.Is(err, ErrUserNotFound), errors.Is(err, ErrIdempotencyConflict):
			u.recordFailure(ctx, adminID, roleName, req, err)
			return payment.LedgerEntry{}, err
		case database.IsUniqueViolationError(err) && uid.IsZero(result.ID):
			// A concurrent transaction committed the same event_key first.
			existing, rerr := u.ledger.FindByEventKey(u.db, ctx, eventKey)
			if rerr != nil {
				return payment.LedgerEntry{}, err
			}
			if !sameHash(existing.RequestHash, requestHash) {
				u.recordFailure(ctx, adminID, roleName, req, ErrIdempotencyConflict)
				return payment.LedgerEntry{}, ErrIdempotencyConflict
			}
			return payment.ToLedgerEntry(existing), nil
		default:
			return payment.LedgerEntry{}, err
		}
	}
	return result, nil
}

func (u *AdjustPointsUsecase) resolveRole(ctx context.Context, adminID uid.ID) (string, error) {
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

// recordFailure writes a best-effort failure audit row for a rejected
// adjustment. It runs in its own transaction because the main transaction
// already rolled back.
func (u *AdjustPointsUsecase) recordFailure(ctx context.Context, adminID uid.ID, roleName string, req PointsAdjustment, reason error) {
	err := database.RunTransaction(ctx, u.db, func(tx *gorm.DB) error {
		return audit.Write(tx, audit.Entry{
			ActorAdminID: &adminID,
			ActorRole:    sameOr(roleName, "admin"),
			Action:       "points.adjust",
			TargetType:   "user",
			TargetID:     &req.UserID,
			Result:       "failure",
			AfterData: map[string]any{
				"user_id": req.UserID,
				"delta":   req.Delta,
				"reason":  reason.Error(),
			},
		})
	})
	if err != nil {
		u.logger.ErrorContext(ctx, "record points.adjust failure audit", "error", err)
	}
}

// translatePaymentError maps the payment domain's balance sentinels onto the
// points adjustment sentinels the handler maps, so the error vocabulary and
// the HTTP-visible messages stay unchanged.
func translatePaymentError(err error) error {
	switch {
	case errors.Is(err, payment.ErrUserNotFound):
		return ErrUserNotFound
	case errors.Is(err, payment.ErrInsufficientBalance):
		return ErrInsufficientPoints
	default:
		return err
	}
}

func validateIdempotencyKey(key string) error {
	if len(key) < 1 || len(key) > 64 || strings.TrimSpace(key) == "" {
		return ErrInvalidIdempotencyKey
	}
	return nil
}

func validateAdjustment(req PointsAdjustment) error {
	if uid.IsZero(req.UserID) || req.Delta == 0 {
		return ErrInvalidAdjustment
	}
	remark := strings.TrimSpace(req.Remark)
	if remark == "" || len([]rune(remark)) > 255 {
		return ErrInvalidAdjustment
	}
	return nil
}

func computeAdjustHash(req PointsAdjustment) string {
	payload := struct {
		UserID uid.ID `json:"user_id"`
		Delta  int64  `json:"delta"`
		Remark string `json:"remark"`
	}{req.UserID, req.Delta, strings.TrimSpace(req.Remark)}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func sameHash(stored *string, current string) bool {
	return stored != nil && *stored == current
}

func sameOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
