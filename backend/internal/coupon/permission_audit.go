package coupon

import (
	"context"
	"log/slog"

	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/audit"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/uid"

	"gorm.io/gorm"
)

// PermissionDeniedAuditor records the failed coupon template creation of an
// administrator whose role lacks coupon:write (specs/coupon/spec.md 无权限创建).
// The permission gate short-circuits before the handler, so the request body is
// never parsed and the usecase never runs: this hook is the only place the
// attempt can be recorded. It satisfies middleware.DeniedAuditor.
type PermissionDeniedAuditor struct {
	db     *gorm.DB
	viewer AdminViewer
	logger *slog.Logger
}

// NewPermissionDeniedAuditor returns the auditor wired to the coupon template
// creation route's permission gate.
func NewPermissionDeniedAuditor(db *gorm.DB, viewer AdminViewer, logger *slog.Logger) *PermissionDeniedAuditor {
	if logger == nil {
		logger = slog.Default()
	}
	return &PermissionDeniedAuditor{db: db, viewer: viewer, logger: logger}
}

// RecordPermissionDenied writes the failure audit row for a denied request. The
// write runs best-effort in its own transaction — no business change accompanies
// it, so nothing else can roll it back — and a failure is only logged: the
// caller still receives the gate's 403, never a 500.
func (a *PermissionDeniedAuditor) RecordPermissionDenied(ctx context.Context, adminID uid.ID, descriptor middleware.PermissionAuditDescriptor) {
	if a == nil || a.db == nil {
		return
	}
	err := database.RunTransaction(ctx, a.db, func(tx *gorm.DB) error {
		return audit.Write(tx, audit.Entry{
			ActorAdminID: &adminID,
			ActorRole:    a.roleNameOf(ctx, adminID),
			Action:       descriptor.Action,
			TargetType:   descriptor.TargetType,
			Result:       "failure",
			AfterData: map[string]any{
				"reason": "insufficient permission",
			},
		})
	})
	if err != nil {
		a.logger.ErrorContext(ctx, "record permission denied audit", "error", err,
			"action", descriptor.Action, "admin_id", adminID.String())
	}
}

// roleNameOf resolves the actor's current role for the audit row, mirroring the
// success path in Handler.resolveActor: an unresolvable role leaves the column
// empty rather than failing the audit write, which must record the actor even
// when the role lookup is unavailable.
func (a *PermissionDeniedAuditor) roleNameOf(ctx context.Context, adminID uid.ID) string {
	if a.viewer == nil {
		return ""
	}
	view, err := a.viewer.FindByID(ctx, adminID)
	if err != nil {
		return ""
	}
	return view.RoleName
}
