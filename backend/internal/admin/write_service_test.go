package admin_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/platform/database"

	"gorm.io/gorm"
)

// roleWriteActorID returns a real super_admin row to act as the audit actor.
// audit_logs carries an FK to admin_users, so the actor id must reference an
// existing administrator.
func roleWriteActorID(t *testing.T, fix *adminFixture) uid.ID {
	t.Helper()
	if id := readAdminID(t, fix.db, "role-write-actor"); !uid.IsZero(id) {
		return id
	}
	result, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username: "role-write-actor", Password: "Sup3rSecret!Pass",
		RoleName: "super_admin", ActorName: "service-test",
	})
	if err != nil {
		t.Fatalf("bootstrap role actor: %v", err)
	}
	return result.AdminID
}

// createRoleInFixture is the shared bootstrap for role-write tests: it seeds a
// real super administrator as the audit actor and creates the named role.
func createRoleInFixture(t *testing.T, fix *adminFixture, name string, permissions []string) admin.CreateRoleResult {
	t.Helper()
	result, err := fix.service.CreateRole(context.Background(), admin.CreateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		Name:         name,
		Permissions:  permissions,
	})
	if err != nil {
		t.Fatalf("create role %q: %v", name, err)
	}
	if uid.IsZero(result.ID) {
		t.Fatal("create role returned zero id")
	}
	return result
}

func TestServiceCreateRoleWritesPermissionsAndAudit(t *testing.T) {
	fix := newAdminFixture(t)
	result := createRoleInFixture(t, fix, "analyst", []string{"product:read", "audit:read", "product:read"})

	if result.Name != "analyst" {
		t.Fatalf("role name = %q, want analyst", result.Name)
	}
	// Duplicate codes are collapsed; the stable input order is preserved.
	if len(result.Permissions) != 2 {
		t.Fatalf("role permissions = %v, want 2 unique codes", result.Permissions)
	}

	roles, err := fix.service.ListRoles(context.Background())
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	var found *admin.RoleView
	for i := range roles {
		if roles[i].Name == "analyst" {
			found = &roles[i]
		}
	}
	if found == nil {
		t.Fatal("created role missing from ListRoles")
	}
	if !containsPermission(found.Permissions, "audit:read") || containsPermission(found.Permissions, "role:manage") {
		t.Fatalf("created role permissions = %v", found.Permissions)
	}

	audit := firstAudit(t, fix.db, "role.create")
	if audit.ActorRole != "super_admin" || audit.TargetType != "role" || audit.Result != "success" {
		t.Fatalf("role.create audit row malformed: %+v", audit)
	}
}

func TestServiceCreateRoleRejectsDuplicateName(t *testing.T) {
	fix := newAdminFixture(t)
	createRoleInFixture(t, fix, "dup_role", []string{"product:read"})
	_, err := fix.service.CreateRole(context.Background(), admin.CreateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		Name:         "dup_role",
		Permissions:  []string{"order:read"},
	})
	if !errors.Is(err, admin.ErrRoleExists) {
		t.Fatalf("duplicate role error = %v, want ErrRoleExists", err)
	}
}

func TestServiceCreateRoleRejectsUnknownPermission(t *testing.T) {
	fix := newAdminFixture(t)
	_, err := fix.service.CreateRole(context.Background(), admin.CreateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		Name:         "bad_perm",
		Permissions:  []string{"product:frobnicate"},
	})
	if !errors.Is(err, admin.ErrUnknownPermission) {
		t.Fatalf("unknown permission error = %v, want ErrUnknownPermission", err)
	}
}

func TestServiceCreateRoleRejectsBlankName(t *testing.T) {
	fix := newAdminFixture(t)
	for _, name := range []string{"", "   ", "this-role-name-is-way-too-long-to-fit-in-the-varchar32-column"} {
		_, err := fix.service.CreateRole(context.Background(), admin.CreateRoleInput{
			ActorAdminID: roleWriteActorID(t, fix),
			ActorRole:    "super_admin",
			Name:         name,
			Permissions:  []string{"product:read"},
		})
		if !errors.Is(err, admin.ErrInvalidRoleName) {
			t.Fatalf("name %q error = %v, want ErrInvalidRoleName", name, err)
		}
	}
}

func TestServiceUpdateRoleReplacesPermissionsAndAudits(t *testing.T) {
	fix := newAdminFixture(t)
	created := createRoleInFixture(t, fix, "perm_role", []string{"product:read", "order:read"})

	beforeCount := auditCountByAction(t, fix.db, "role.update")
	result, err := fix.service.UpdateRole(context.Background(), admin.UpdateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		RoleID:       created.ID,
		Permissions:  &[]string{"refund:read", "unknown:perm"},
	})
	if !errors.Is(err, admin.ErrUnknownPermission) {
		t.Fatalf("update to unknown permission error = %v, want ErrUnknownPermission", err)
	}

	result, err = fix.service.UpdateRole(context.Background(), admin.UpdateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		RoleID:       created.ID,
		Permissions:  &[]string{"refund:read"},
	})
	if err != nil {
		t.Fatalf("update role: %v", err)
	}
	if len(result.Permissions) != 1 || result.Permissions[0] != "refund:read" {
		t.Fatalf("updated permissions = %v, want [refund:read]", result.Permissions)
	}
	roles, _ := fix.service.ListRoles(context.Background())
	for i := range roles {
		if roles[i].ID == created.ID {
			if !containsPermission(roles[i].Permissions, "refund:read") || containsPermission(roles[i].Permissions, "order:read") {
				t.Fatalf("role permissions after update = %v", roles[i].Permissions)
			}
		}
	}
	if got := auditCountByAction(t, fix.db, "role.update") - beforeCount; got != 1 {
		t.Fatalf("role.update audit delta = %d, want 1", got)
	}
}

func TestServiceUpdateRoleRenamesAndRejectsCollision(t *testing.T) {
	fix := newAdminFixture(t)
	created := createRoleInFixture(t, fix, "rename_me", []string{"product:read"})

	result, err := fix.service.UpdateRole(context.Background(), admin.UpdateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		RoleID:       created.ID,
		Name:         adminStringPtr("rename_done"),
	})
	if err != nil {
		t.Fatalf("rename role: %v", err)
	}
	if result.Name != "rename_done" {
		t.Fatalf("renamed role name = %q, want rename_done", result.Name)
	}

	// Renaming to an existing seeded role name collides.
	_, err = fix.service.UpdateRole(context.Background(), admin.UpdateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		RoleID:       created.ID,
		Name:         adminStringPtr("operator"),
	})
	if !errors.Is(err, admin.ErrRoleExists) {
		t.Fatalf("collision error = %v, want ErrRoleExists", err)
	}
}

func TestServiceUpdateRoleRejectsMissingRoleAndEmptyPayload(t *testing.T) {
	fix := newAdminFixture(t)
	_, err := fix.service.UpdateRole(context.Background(), admin.UpdateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		RoleID:       uid.ID{},
		Permissions:  &[]string{"product:read"},
	})
	if !errors.Is(err, admin.ErrRoleNotFound) {
		t.Fatalf("missing role error = %v, want ErrRoleNotFound", err)
	}
	_, err = fix.service.UpdateRole(context.Background(), admin.UpdateRoleInput{
		ActorAdminID: roleWriteActorID(t, fix),
		ActorRole:    "super_admin",
		RoleID:       uid.ID{},
	})
	if !errors.Is(err, admin.ErrRoleUpdateEmpty) {
		t.Fatalf("empty payload error = %v, want ErrRoleUpdateEmpty", err)
	}
}

func TestServiceListUsersSearchByIdAndNickname(t *testing.T) {
	fix := newAdminFixture(t)
	member := database.User{OpenID: "svc-user-1-openid", Nickname: "member-alfa", PointsBalance: 50}
	if err := fix.db.WithContext(context.Background()).Create(&member).Error; err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if err := fix.db.WithContext(context.Background()).Create(&database.User{
		OpenID: "svc-user-2-openid", Nickname: "member-beta", PointsBalance: 70,
	}).Error; err != nil {
		t.Fatalf("seed member: %v", err)
	}

	all, total, err := fix.service.ListUsers(context.Background(), 1, 10, "")
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if total < 2 || len(all) < 2 {
		t.Fatalf("users total=%d list=%d, want at least 2", total, len(all))
	}

	// A canonical uuid keyword resolves the exact id.
	byID, totalID, err := fix.service.ListUsers(context.Background(), 1, 10, member.ID.String())
	if err != nil {
		t.Fatalf("list users by id: %v", err)
	}
	if totalID != 2 || len(byID) != 1 || byID[0].ID != member.ID {
		t.Fatalf("user by id = %+v (total %d), want %s", byID, totalID, member.ID)
	}

	// Text keyword is a case-insensitive nickname substring.
	byName, totalName, err := fix.service.ListUsers(context.Background(), 1, 10, "ALFA")
	if err != nil {
		t.Fatalf("list users by nickname: %v", err)
	}
	if totalName != 1 || len(byName) != 1 || byName[0].Nickname != "member-alfa" {
		t.Fatalf("user by nickname = %+v (total %d)", byName, totalName)
	}
}

func TestServiceListAuditLogsOrderedDescending(t *testing.T) {
	fix := newAdminFixture(t)
	createRoleInFixture(t, fix, "audit_role", []string{"product:read"})
	createRoleInFixture(t, fix, "audit_role_2", []string{"product:read"})

	logs, total, err := fix.service.ListAuditLogs(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if total < 2 {
		t.Fatalf("audit total = %d, want at least 2", total)
	}
	if len(logs) < 2 {
		t.Fatalf("audit list = %d, want at least 2", len(logs))
	}
	prev := logs[0].ID
	for _, entry := range logs[1:] {
		if bytes.Compare(entry.ID[:], prev[:]) > 0 {
			t.Fatalf("audit order not descending: %s then %s", prev, entry.ID)
		}
		prev = entry.ID
	}
	// The newest entries are the role creates created above.
	if logs[0].Action != "role.create" && logs[1].Action != "role.create" {
		t.Fatalf("expected role.create near the top, got %s;%s", logs[0].Action, logs[1].Action)
	}
}

func adminStringPtr(value string) *string { return &value }

func auditCountByAction(t *testing.T, db *gorm.DB, action string) int {
	t.Helper()
	var count int
	if err := db.Raw(`SELECT count(*) FROM audit_logs WHERE action = ?`, action).Scan(&count).Error; err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	return count
}
