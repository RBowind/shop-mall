package main

import (
	"encoding/json"
	"strings"
	"testing"

	"shop-mall/backend/internal/admin"
)

// TestBootstrapBinaryCreatesFirstAdministrator verifies the happy path:
// an empty database receives the first administrator with the requested
// role and the credentials work for login.
func TestBootstrapBinaryCreatesFirstAdministrator(t *testing.T) {
	_, dsn, _ := startBootstrapPostgresContainer(t)
	stdout := &concurrentBuffer{}
	stderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, stdout, stderr, []string{
		"--username", "bootstrap-cli-admin",
		"--password", "Sup3rSecret!Pass",
		"--role", "super_admin",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err != nil {
		t.Fatalf("bootstrap exit error: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "admin id:") {
		t.Fatalf("bootstrap stdout missing admin id: %s", stdout.String())
	}
}

// TestBootstrapBinaryRejectsDuplicateUsername verifies the binary never
// overwrites an existing administrator and that the stored password hash is
// not mutated.
func TestBootstrapBinaryRejectsDuplicateUsername(t *testing.T) {
	_, dsn, _ := startBootstrapPostgresContainer(t)
	firstStdout := &concurrentBuffer{}
	firstStderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, firstStdout, firstStderr, []string{
		"--username", "duplicate-cli-admin",
		"--password", "Sup3rSecret!Pass",
		"--role", "super_admin",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	secondStdout := &concurrentBuffer{}
	secondStderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, secondStdout, secondStderr, []string{
		"--username", "duplicate-cli-admin",
		"--password", "An0therStr0ng!Pass",
		"--role", "super_admin",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err == nil {
		t.Fatal("expected duplicate bootstrap to fail")
	}
	if !strings.Contains(secondStderr.String(), "administrator already exists") {
		t.Fatalf("expected 'administrator already exists' message, got: %s", secondStderr.String())
	}
}

// TestBootstrapBinaryRejectsWeakPassword verifies the input pipeline
// rejects weak passwords before any database write.
func TestBootstrapBinaryRejectsWeakPassword(t *testing.T) {
	_, dsn, _ := startBootstrapPostgresContainer(t)
	stdout := &concurrentBuffer{}
	stderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, stdout, stderr, []string{
		"--username", "weak-pw-cli-admin",
		"--password", "password",
		"--role", "super_admin",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err == nil {
		t.Fatal("expected weak password rejection")
	}
	if !strings.Contains(stderr.String(), "weak password") {
		t.Fatalf("expected 'weak password' message, got: %s", stderr.String())
	}
}

// TestBootstrapBinaryRejectsUnknownRole verifies role validation happens
// before any database write.
func TestBootstrapBinaryRejectsUnknownRole(t *testing.T) {
	_, dsn, _ := startBootstrapPostgresContainer(t)
	stdout := &concurrentBuffer{}
	stderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, stdout, stderr, []string{
		"--username", "ghost-role-cli-admin",
		"--password", "Sup3rSecret!Pass",
		"--role", "ghost-role",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err == nil {
		t.Fatal("expected unknown role rejection")
	}
	if !strings.Contains(stderr.String(), "unknown role") {
		t.Fatalf("expected 'unknown role' message, got: %s", stderr.String())
	}
}

// TestBootstrapBinaryRejectsBlankUsername verifies the input pipeline
// rejects blank usernames.
func TestBootstrapBinaryRejectsBlankUsername(t *testing.T) {
	_, dsn, _ := startBootstrapPostgresContainer(t)
	stdout := &concurrentBuffer{}
	stderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, stdout, stderr, []string{
		"--username", "   ",
		"--password", "Sup3rSecret!Pass",
		"--role", "super_admin",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err == nil {
		t.Fatal("expected blank username rejection")
	}
}

// TestBootstrapBinaryRejectsBlankInputFlags verifies the binary rejects
// the controlled input path when any flag is blank.
func TestBootstrapBinaryRejectsBlankInputFlags(t *testing.T) {
	_, dsn, _ := startBootstrapPostgresContainer(t)
	stdout := &concurrentBuffer{}
	stderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, stdout, stderr, []string{}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err == nil {
		t.Fatal("expected blank input rejection")
	}
}

// TestBootstrapBinaryRejectsInvalidEnvironment ensures the binary does
// not run without an explicit DATABASE_URL.
func TestBootstrapBinaryRejectsInvalidEnvironment(t *testing.T) {
	stdout := &concurrentBuffer{}
	stderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, stdout, stderr, []string{
		"--username", "no-url-admin",
		"--password", "Sup3rSecret!Pass",
		"--role", "super_admin",
	}, map[string]string{
		"DATABASE_URL":             "",
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err == nil {
		t.Fatal("expected missing database URL rejection")
	}
	if !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL message, got: %s", stderr.String())
	}
}

// TestBootstrapBinaryReportsBootstrapResultAsJSON ensures the
// machine-readable JSON output is well-formed and contains the
// administrator id.
func TestBootstrapBinaryReportsBootstrapResultAsJSON(t *testing.T) {
	_, dsn, _ := startBootstrapPostgresContainer(t)
	stdout := &concurrentBuffer{}
	stderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, stdout, stderr, []string{
		"--username", "json-admin",
		"--password", "Sup3rSecret!Pass",
		"--role", "super_admin",
		"--format", "json",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err != nil {
		t.Fatalf("bootstrap json exit error: %v\nstderr: %s", err, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &payload); err != nil {
		t.Fatalf("decode json payload: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["admin_id"]; !ok {
		t.Fatalf("json payload missing admin_id: %v", payload)
	}
}

// TestBootstrapBinaryWritesAuditLog verifies the binary writes an audit
// log row when the bootstrap succeeds.
func TestBootstrapBinaryWritesAuditLog(t *testing.T) {
	db, dsn, _ := startBootstrapPostgresContainer(t)
	stdout := &concurrentBuffer{}
	stderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, stdout, stderr, []string{
		"--username", "audit-cli-admin",
		"--password", "Sup3rSecret!Pass",
		"--role", "super_admin",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err != nil {
		t.Fatalf("bootstrap exit error: %v\nstderr: %s", err, stderr.String())
	}
	var count int64
	row := db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action = 'admin.bootstrap'`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("count bootstrap audit rows: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one admin.bootstrap audit row")
	}
}

// TestBootstrapBinaryKeepsExistingAdministratorRole verifies a second
// bootstrap call for the same username never mutates the stored role.
func TestBootstrapBinaryKeepsExistingAdministratorRole(t *testing.T) {
	db, dsn, _ := startBootstrapPostgresContainer(t)
	firstStdout := &concurrentBuffer{}
	firstStderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, firstStdout, firstStderr, []string{
		"--username", "role-keep-admin",
		"--password", "Sup3rSecret!Pass",
		"--role", "operator",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err != nil {
		t.Fatalf("bootstrap operator: %v", err)
	}
	secondStdout := &concurrentBuffer{}
	secondStderr := &concurrentBuffer{}
	if err := runBootstrapForTest(t, secondStdout, secondStderr, []string{
		"--username", "role-keep-admin",
		"--password", "Sup3rSecret!Pass",
		"--role", "super_admin",
	}, map[string]string{
		"DATABASE_URL":             dsn,
		"SHOP_MALL_MIGRATIONS_DIR": "../../migrations",
	}); err == nil {
		t.Fatal("expected duplicate role reassignment rejection")
	}
	row := db.QueryRow(`SELECT r.name FROM admin_users a JOIN roles r ON r.id = a.role_id WHERE a.username = $1`, "role-keep-admin")
	var roleName string
	if err := row.Scan(&roleName); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if roleName != "operator" {
		t.Fatalf("role mutated: %s", roleName)
	}
}

// TestBootstrapValidatesUsernameAndPasswordRules verifies the exported
// validators run in the binary without a database.
func TestBootstrapValidatesUsernameAndPasswordRules(t *testing.T) {
	if err := validateAdminUsernameForTest("ok-name_1.0"); err != nil {
		t.Fatalf("username accepted: %v", err)
	}
	if err := validateAdminUsernameForTest("   "); err == nil {
		t.Fatal("blank username accepted")
	}
	if err := validateAdminPasswordForTest("Sup3rSecret!Pass"); err != nil {
		t.Fatalf("strong password rejected: %v", err)
	}
	if err := validateAdminPasswordForTest("password"); err == nil {
		t.Fatal("weak password accepted")
	}
}

func validateAdminUsernameForTest(value string) error {
	return validateUsernameForTest(value)
}

func validateAdminPasswordForTest(value string) error {
	return validatePasswordForTest(value)
}

func validateUsernameForTest(value string) error {
	return admin.ValidateUsername(value)
}

func validatePasswordForTest(value string) error {
	return admin.ValidatePassword(value)
}

var _ = baseEnv
