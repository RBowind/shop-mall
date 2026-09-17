package coupon_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/coupon"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/uid"
)

func TestRecordPermissionDeniedWritesFailureRowWithResolvedRole(t *testing.T) {
	fix := newCouponFixture(t)
	descriptor := middleware.PermissionAuditDescriptor{Action: "coupon_template.create", TargetType: "coupon_template"}

	fix.auditor.RecordPermissionDenied(context.Background(), fix.readlessAdmin.id, descriptor)

	var row struct {
		Action     string
		TargetType string
		Result     string
		ActorAdmin uid.ID
		ActorRole  string
		AfterData  string
	}
	if err := fix.db.Raw(
		`SELECT action, target_type, result, actor_admin_id AS actor_admin, actor_role, after_data::text AS after_data
		 FROM audit_logs WHERE action = ? AND actor_admin_id = ?`, descriptor.Action, fix.readlessAdmin.id,
	).Row().Scan(&row.Action, &row.TargetType, &row.Result, &row.ActorAdmin, &row.ActorRole, &row.AfterData); err != nil {
		t.Fatalf("read denial audit row: %v", err)
	}
	if row.Result != "failure" || row.TargetType != "coupon_template" {
		t.Fatalf("row = %q/%q, want failure/coupon_template", row.Result, row.TargetType)
	}
	if row.ActorAdmin != fix.readlessAdmin.id {
		t.Fatalf("row actor = %s, want the denied administrator %s", row.ActorAdmin, fix.readlessAdmin.id)
	}
	if row.ActorRole != fix.readlessAdmin.roleName {
		t.Fatalf("row actor role = %q, want %q", row.ActorRole, fix.readlessAdmin.roleName)
	}
	if !strings.Contains(row.AfterData, "insufficient permission") {
		t.Fatalf("row after image = %s, want the denial reason", row.AfterData)
	}
}

// TestRecordPermissionDeniedToleratesNilDependencies pins the best-effort
// guarantee: an auditor with no database, no viewer or a nil receiver records
// nothing and never panics, so the caller keeps the gate's 403.
func TestRecordPermissionDeniedToleratesNilDependencies(t *testing.T) {
	fix := newCouponFixture(t)
	descriptor := middleware.PermissionAuditDescriptor{Action: "coupon_template.create", TargetType: "coupon_template"}

	var nilAuditor *coupon.PermissionDeniedAuditor
	nilAuditor.RecordPermissionDenied(context.Background(), fix.superAdmin.id, descriptor)

	noDB := coupon.NewPermissionDeniedAuditor(nil, fix.adminService, nil)
	noDB.RecordPermissionDenied(context.Background(), fix.superAdmin.id, descriptor)

	// A viewer-less auditor still records the actor; only the role column is
	// left empty.
	noViewer := coupon.NewPermissionDeniedAuditor(fix.db, nil, slog.Default())
	noViewer.RecordPermissionDenied(context.Background(), fix.superAdmin.id, descriptor)

	var row struct {
		ActorAdmin uid.ID
		ActorRole  string
	}
	if err := fix.db.Raw(
		`SELECT actor_admin_id AS actor_admin, actor_role FROM audit_logs WHERE action = ? AND actor_admin_id = ?`,
		descriptor.Action, fix.superAdmin.id,
	).Row().Scan(&row.ActorAdmin, &row.ActorRole); err != nil {
		t.Fatalf("read denial audit row: %v", err)
	}
	if row.ActorAdmin != fix.superAdmin.id {
		t.Fatalf("row actor = %s, want %s", row.ActorAdmin, fix.superAdmin.id)
	}
	if row.ActorRole != "" {
		t.Fatalf("row actor role = %q, want empty when no viewer is wired", row.ActorRole)
	}

	var written int64
	if err := fix.db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = ? AND actor_admin_id = ?`, descriptor.Action, fix.superAdmin.id,
	).Scan(&written).Error; err != nil {
		t.Fatalf("count denial audits: %v", err)
	}
	if written != 1 {
		t.Fatalf("denial audit rows = %d, want exactly the one written by the viewer-less auditor", written)
	}
}

// TestRecordPermissionDeniedSwallowsWriteFailure covers the failed-write path:
// the audit write runs in its own transaction, and a failure is logged and
// swallowed rather than returned, so the caller still answers the gate's 403
// instead of turning the denial into a 500. The write is made to fail by hiding
// the append-only audit table, which keeps the connection pool (and therefore
// the "nothing landed" read below) usable.
func TestRecordPermissionDeniedSwallowsWriteFailure(t *testing.T) {
	fix := newCouponFixture(t)
	descriptor := middleware.PermissionAuditDescriptor{Action: "coupon_template.create", TargetType: "coupon_template"}
	var logged bytes.Buffer
	auditor := coupon.NewPermissionDeniedAuditor(fix.db, fix.adminService,
		slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn})))

	fix.withTableHidden(t, "audit_logs", func() {
		auditor.RecordPermissionDenied(context.Background(), fix.superAdmin.id, descriptor)
	})

	// The failure is reported, and the report names the attempt it failed to
	// record.
	if !strings.Contains(logged.String(), "record permission denied audit") {
		t.Fatalf("log = %q, want the failed audit write reported", logged.String())
	}
	if !strings.Contains(logged.String(), descriptor.Action) {
		t.Fatalf("log = %q, want the denied action %q named", logged.String(), descriptor.Action)
	}
	if !strings.Contains(logged.String(), fix.superAdmin.id.String()) {
		t.Fatalf("log = %q, want the denied administrator %s named", logged.String(), fix.superAdmin.id)
	}

	// Nothing was recorded: the failed hook leaves no row behind.
	var written int64
	if err := fix.db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = ? AND actor_admin_id = ?`, descriptor.Action, fix.superAdmin.id,
	).Scan(&written).Error; err != nil {
		t.Fatalf("count denial audits: %v", err)
	}
	if written != 0 {
		t.Fatalf("denial audit rows = %d, want 0 after a failed write", written)
	}
}

// failingAdminViewer is an AdminViewer whose role lookup is unavailable, which
// is the state the denial audit has to survive: the actor is recorded even when
// the role cannot be resolved.
type failingAdminViewer struct{}

func (failingAdminViewer) FindByID(context.Context, uid.ID) (admin.AdminView, error) {
	return admin.AdminView{}, errors.New("admin lookup unavailable")
}

func TestRecordPermissionDeniedKeepsTheRowWhenTheRoleLookupFails(t *testing.T) {
	fix := newCouponFixture(t)
	auditor := coupon.NewPermissionDeniedAuditor(fix.db, failingAdminViewer{}, nil)
	descriptor := middleware.PermissionAuditDescriptor{Action: "coupon_template.status_change", TargetType: "coupon_template"}

	auditor.RecordPermissionDenied(context.Background(), fix.superAdmin.id, descriptor)

	var row struct {
		ActorAdmin uid.ID
		ActorRole  string
		Result     string
	}
	if err := fix.db.Raw(
		`SELECT actor_admin_id AS actor_admin, actor_role, result FROM audit_logs WHERE action = ? AND actor_admin_id = ?`,
		descriptor.Action, fix.superAdmin.id,
	).Row().Scan(&row.ActorAdmin, &row.ActorRole, &row.Result); err != nil {
		t.Fatalf("read denial audit row: %v", err)
	}
	if row.ActorAdmin != fix.superAdmin.id || row.Result != "failure" {
		t.Fatalf("row = %s/%q, want the denied actor with a failure result", row.ActorAdmin, row.Result)
	}
	if row.ActorRole != "" {
		t.Fatalf("row actor role = %q, want empty when the lookup fails", row.ActorRole)
	}
}
