package access_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/application/access"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/tests/fixtures"
	"shop-mall/backend/tests/integration"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type accessFixture struct {
	db       *gorm.DB
	usecase  *access.AccessUsecase
	adminSvc *admin.Service
	logBuf   *concurrentBuffer
	now      time.Time
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logBuf := &concurrentBuffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	adminService, err := admin.NewService(admin.ServiceDeps{
		DB:     db,
		Logger: logger,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new admin service: %v", err)
	}
	usecase, err := access.NewAccessUsecase(access.AccessUsecaseDeps{
		DB:      db,
		Service: adminService,
		Logger:  logger,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new access usecase: %v", err)
	}
	return &accessFixture{db: db, usecase: usecase, adminSvc: adminService, logBuf: logBuf, now: now}
}

func (f *accessFixture) seedSuperAdmin(t *testing.T, username string) uid.ID {
	t.Helper()
	if _, err := f.adminSvc.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  username,
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap %s: %v", username, err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM admin_users WHERE username = ?`, username).Row().Scan(&id); err != nil {
		t.Fatalf("lookup %s: %v", username, err)
	}
	return id
}

func TestAccessUsecaseChangeRoleReassignsOperatorAndAuditLogsBeforeAfter(t *testing.T) {
	fix := newAccessFixture(t)
	actorID := fix.seedSuperAdmin(t, "actor-super")
	targetID := fix.seedSuperAdmin(t, "target-admin")

	updated, err := fix.usecase.ChangeRole(context.Background(), access.ChangeRoleInput{
		ActorAdminID: actorID,
		ActorRole:    "super_admin",
		TargetID:     targetID,
		NewRoleName:  "operator",
	})
	if err != nil {
		t.Fatalf("change role: %v", err)
	}
	if updated.RoleName != "operator" {
		t.Fatalf("role = %q, want operator", updated.RoleName)
	}
	var dbRoleID uid.ID
	if err := fix.db.Raw(`SELECT role_id FROM admin_users WHERE id = ?`, targetID).Row().Scan(&dbRoleID); err != nil {
		t.Fatalf("lookup role_id: %v", err)
	}
	if uid.IsZero(dbRoleID) {
		t.Fatal("role_id is zero after change_role")
	}

	row := firstAudit(t, fix.db, "admin.role.change")
	if row.Result != "success" {
		t.Fatalf("audit result = %q, want success", row.Result)
	}
	if row.ActorAdminID == nil || *row.ActorAdminID != actorID {
		t.Fatalf("audit actor = %v, want %d", row.ActorAdminID, actorID)
	}
	if row.AfterData == nil {
		t.Fatal("audit after_data is nil")
	}
	var after map[string]any
	if err := json.Unmarshal(row.AfterData, &after); err != nil {
		t.Fatalf("decode after_data: %v", err)
	}
	if after["role_name"] != "operator" {
		t.Fatalf("audit after_data.role_name = %v, want operator", after["role_name"])
	}
}

func TestAccessUsecaseUpdateAdminDisablesAdministratorAndAuditLogs(t *testing.T) {
	fix := newAccessFixture(t)
	actorID := fix.seedSuperAdmin(t, "actor-super-2")
	targetID := fix.seedSuperAdmin(t, "target-disable")

	updated, err := fix.usecase.UpdateAdmin(context.Background(), access.UpdateAdminInput{
		ActorAdminID: actorID,
		ActorRole:    "super_admin",
		TargetID:     targetID,
		Enabled:      boolPtr(false),
	})
	if err != nil {
		t.Fatalf("update admin: %v", err)
	}
	if updated.Enabled {
		t.Fatal("admin still enabled after disable")
	}
	row := firstAudit(t, fix.db, "admin.update")
	if row.Result != "success" {
		t.Fatalf("audit result = %q, want success", row.Result)
	}
	if row.ActorAdminID == nil || *row.ActorAdminID != actorID {
		t.Fatalf("audit actor mismatch")
	}
}

func TestAccessUsecaseChangePasswordBumpsTokenVersionAndInvalidatesOldJWT(t *testing.T) {
	fix := newAccessFixture(t)
	actorID := fix.seedSuperAdmin(t, "actor-pw")
	targetID := fix.seedSuperAdmin(t, "target-pw")
	cfg := config.JWTConfig{
		Issuer:    "admin-test-issuer",
		Audience:  "admin-test-audience",
		TTL:       30 * time.Minute,
		ActiveKID: "admin-test-kid",
		Keys: map[string][]byte{
			"admin-test-kid": []byte("admin-test-key-material-with-at-least-32-bytes"),
		},
	}
	signer, err := tokens.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	var oldVersion int64
	if err := fix.db.Raw(`SELECT token_version FROM admin_users WHERE id = ?`, targetID).Scan(&oldVersion).Error; err != nil {
		t.Fatalf("read token version: %v", err)
	}
	subject := "admin:" + targetID.String()
	oldToken, err := signer.Issue(subject, oldVersion, fix.now)
	if err != nil {
		t.Fatalf("issue old token: %v", err)
	}
	if _, err := signer.Parse(oldToken, fix.now); err != nil {
		t.Fatalf("parse old token before change: %v", err)
	}

	updated, err := fix.usecase.ChangePassword(context.Background(), access.ChangePasswordInput{
		ActorAdminID: targetID,
		ActorRole:    "self",
		TargetID:     targetID,
		OldPassword:  "Sup3rSecret!Pass",
		NewPassword:  "Sup3rSecret!NewPass",
	})
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if updated.TokenVersion <= oldVersion {
		t.Fatalf("token version did not increase: old=%d new=%d", oldVersion, updated.TokenVersion)
	}
	oldClaims, err := signer.Parse(oldToken, fix.now)
	if err != nil {
		t.Fatalf("parse old token: %v", err)
	}
	ok, err := fix.adminSvc.IsTokenVersionCurrent(context.Background(), targetID, oldClaims.TokenVersion)
	if err != nil {
		t.Fatalf("check token version: %v", err)
	}
	if ok {
		t.Fatal("old token version should no longer be current after password change")
	}
	newToken, err := signer.Issue(subject, updated.TokenVersion, fix.now)
	if err != nil {
		t.Fatalf("issue new token: %v", err)
	}
	if _, err := signer.Parse(newToken, fix.now); err != nil {
		t.Fatalf("new token should be valid: %v", err)
	}
	row := firstAudit(t, fix.db, "admin.password.change")
	if row.Result != "success" {
		t.Fatalf("audit result = %q, want success", row.Result)
	}
	if row.ActorAdminID == nil || *row.ActorAdminID != targetID {
		t.Fatalf("audit actor mismatch")
	}
	_ = actorID
}

func TestAccessUsecaseChangeRoleRejectsUnknownPermission(t *testing.T) {
	fix := newAccessFixture(t)
	actorID := fix.seedSuperAdmin(t, "actor-unknown-perm")
	targetID := fix.seedSuperAdmin(t, "target-unknown-perm")
	if err := fix.db.Exec(`INSERT INTO permissions (id, code, name) VALUES (?, 'unknown:perm', 'Unknown')`, uid.New()).Error; err != nil {
		t.Fatalf("insert unknown permission: %v", err)
	}
	var roleID uid.ID
	if err := fix.db.Raw(`SELECT id FROM roles WHERE name = 'operator'`).Row().Scan(&roleID); err != nil {
		t.Fatalf("read operator role: %v", err)
	}
	var permID uid.ID
	if err := fix.db.Raw(`SELECT id FROM permissions WHERE code = 'unknown:perm'`).Row().Scan(&permID); err != nil {
		t.Fatalf("read unknown permission: %v", err)
	}
	if err := fix.db.Exec(`INSERT INTO role_permissions (role_id, permission_id) VALUES (?, ?)`, roleID, permID).Error; err != nil {
		t.Fatalf("bind unknown permission to operator role: %v", err)
	}
	if _, err := fix.usecase.ChangeRole(context.Background(), access.ChangeRoleInput{
		ActorAdminID: actorID,
		ActorRole:    "super_admin",
		TargetID:     targetID,
		NewRoleName:  "operator",
	}); err == nil {
		t.Fatal("expected error when assigning an unknown permission")
	}
	row := firstAudit(t, fix.db, "admin.role.change")
	if row.Result != "failure" {
		t.Fatalf("audit result = %q, want failure", row.Result)
	}
}

func TestAccessUsecaseRefusesToChangeRoleForUnknownTarget(t *testing.T) {
	fix := newAccessFixture(t)
	actorID := fix.seedSuperAdmin(t, "actor-unknown-target")
	if _, err := fix.usecase.ChangeRole(context.Background(), access.ChangeRoleInput{
		ActorAdminID: actorID,
		ActorRole:    "super_admin",
		TargetID:     uid.ID{},
		NewRoleName:  "operator",
	}); err == nil {
		t.Fatal("expected error when changing role of a missing admin")
	}
}

func TestAccessUsecaseRejectsNonExistentRole(t *testing.T) {
	fix := newAccessFixture(t)
	actorID := fix.seedSuperAdmin(t, "actor-bad-role")
	targetID := fix.seedSuperAdmin(t, "target-bad-role")
	if _, err := fix.usecase.ChangeRole(context.Background(), access.ChangeRoleInput{
		ActorAdminID: actorID,
		ActorRole:    "super_admin",
		TargetID:     targetID,
		NewRoleName:  "ghost-role",
	}); err == nil {
		t.Fatal("expected error for non-existent role")
	}
}

func TestAccessUsecaseAuditLogsAppendOnly(t *testing.T) {
	fix := newAccessFixture(t)
	actorID := fix.seedSuperAdmin(t, "actor-audit")
	targetID := fix.seedSuperAdmin(t, "target-audit")
	if _, err := fix.usecase.UpdateAdmin(context.Background(), access.UpdateAdminInput{
		ActorAdminID: actorID,
		ActorRole:    "super_admin",
		TargetID:     targetID,
		Enabled:      boolPtr(false),
	}); err != nil {
		t.Fatalf("disable admin: %v", err)
	}
	row := firstAudit(t, fix.db, "admin.update")
	if err := fix.db.Exec(`UPDATE audit_logs SET action = 'tampered' WHERE id = ?`, row.ID).Error; err == nil {
		t.Fatal("audit_logs row was updatable; trigger should reject the change")
	} else if !isPostgresTriggerError(err) {
		t.Fatalf("unexpected error updating audit_logs: %v", err)
	}
}

func TestAccessUsecaseConcurrentChangeRoleIsConsistent(t *testing.T) {
	fix := newAccessFixture(t)
	actorID := fix.seedSuperAdmin(t, "actor-concurrent")
	targetID := fix.seedSuperAdmin(t, "target-concurrent")
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = fix.usecase.ChangeRole(context.Background(), access.ChangeRoleInput{
				ActorAdminID: actorID,
				ActorRole:    "super_admin",
				TargetID:     targetID,
				NewRoleName:  "operator",
			})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, errPermissionDrift) && !strings.Contains(err.Error(), "deadlock") {
			t.Fatalf("unexpected concurrent change_role error: %v", err)
		}
	}
	var successCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM audit_logs WHERE action = 'admin.role.change' AND result = 'success'`).Scan(&successCount).Error; err != nil {
		t.Fatalf("count success audit: %v", err)
	}
	if successCount == 0 {
		t.Fatal("expected at least one successful change_role audit log")
	}
}

var errPermissionDrift = errors.New("permission drift")

func firstAudit(t *testing.T, db *gorm.DB, action string) auditRow {
	t.Helper()
	var row auditRow
	if err := db.Raw(`SELECT id, actor_admin_id, actor_role, action, target_type, result, after_data FROM audit_logs WHERE action = ? ORDER BY id DESC LIMIT 1`, action).Scan(&row).Error; err != nil {
		t.Fatalf("lookup audit %s: %v", action, err)
	}
	if uid.IsZero(row.ID) {
		t.Fatalf("audit log for %s not found", action)
	}
	return row
}

type auditRow struct {
	ID           uid.ID
	ActorAdminID *uid.ID
	ActorRole    string
	Action       string
	TargetType   string
	Result       string
	AfterData    datatypes.JSON
}

func isPostgresTriggerError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "audit_logs is append-only") || strings.Contains(err.Error(), "P0001")
}

func boolPtr(value bool) *bool { return &value }

type concurrentBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *concurrentBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *concurrentBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

var _ = fixtures.Snapshot
