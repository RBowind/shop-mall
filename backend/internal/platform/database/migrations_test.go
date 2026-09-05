package database

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestMigrationFilesExist(t *testing.T) {
	root := backendRoot(t)
	for _, name := range []string{"0001_init.up.sql", "0002_seed.up.sql"} {
		if _, err := os.Stat(filepath.Join(root, "migrations", name)); err != nil {
			t.Fatalf("migration %s is missing: %v", name, err)
		}
	}
}

func TestMigrationsApplyAndEnforceConstraints(t *testing.T) {
	db := openMigrationDatabase(t)
	applyMigration(t, db, "0001_init.up.sql")
	applyMigration(t, db, "0002_seed.up.sql")

	assertSeed(t, db)
	seed := readMigration(t, "0002_seed.up.sql")
	if strings.Contains(strings.ToLower(seed), "password") || strings.Contains(seed, "os.Getenv") {
		t.Fatal("seed migration must not contain password or environment access")
	}
	if err := db.Exec(seed).Error; err != nil {
		t.Fatalf("seed migration is not idempotent: %v", err)
	}

	fixture := createFixture(t, db)
	t.Run("product address cart and item constraints", func(t *testing.T) {
		fixture.orderID = insertOrder(t, db, fixture, "ORD-000", "client-000", "paid", "")
		expectConstraintViolation(t, db, "23514", "products_price_points_positive", `INSERT INTO products (id, name, price_points, stock) VALUES (?, 'Invalid Price', 0, 1)`, uid.New())
		expectConstraintViolation(t, db, "23514", "products_stock_nonnegative", `INSERT INTO products (id, name, price_points, stock) VALUES (?, 'Invalid Stock', 1, -1)`, uid.New())
		expectConstraintViolation(t, db, "23514", "products_status_valid", `INSERT INTO products (id, name, price_points, stock, status) VALUES (?, 'Invalid Status', 1, 1, 'deleted')`, uid.New())
		expectConstraintViolation(t, db, "23503", "user_addresses_user_id_fk", `INSERT INTO user_addresses (id, user_id, receiver, phone, region, detail) VALUES (?, '00000000-0000-0000-0000-000000000000', 'Buyer', '13800000000', 'Region', 'Detail')`, uid.New())
		expectConstraintViolation(t, db, "23514", "user_addresses_version_positive", `INSERT INTO user_addresses (id, user_id, receiver, phone, region, detail, version) VALUES (?, ?, 'Buyer', '13800000000', 'Region', 'Detail', 0)`, uid.New(), fixture.userID)
		if err := db.Exec(`INSERT INTO user_addresses (id, user_id, receiver, phone, region, detail, is_default) VALUES (?, ?, 'Buyer', '13800000000', 'Region', 'Detail', true)`, uid.New(), fixture.userID).Error; err != nil {
			t.Fatalf("insert default address: %v", err)
		}
		expectConstraintViolation(t, db, "23505", "uq_default_address", `INSERT INTO user_addresses (id, user_id, receiver, phone, region, detail, is_default) VALUES (?, ?, 'Buyer 2', '13800000001', 'Region', 'Detail 2', true)`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23503", "cart_items_product_id_fk", `INSERT INTO cart_items (id, user_id, product_id, quantity) VALUES (?, ?, '00000000-0000-0000-0000-000000000000', 1)`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23514", "cart_items_quantity_positive", `INSERT INTO cart_items (id, user_id, product_id, quantity) VALUES (?, ?, ?, 0)`, uid.New(), fixture.userID, fixture.productID)
		if err := db.Exec(`INSERT INTO cart_items (id, user_id, product_id, quantity) VALUES (?, ?, ?, 1)`, uid.New(), fixture.userID, fixture.productID).Error; err != nil {
			t.Fatalf("insert cart item: %v", err)
		}
		expectConstraintViolation(t, db, "23505", "cart_items_user_id_product_id_key", `INSERT INTO cart_items (id, user_id, product_id, quantity) VALUES (?, ?, ?, 2)`, uid.New(), fixture.userID, fixture.productID)
		expectConstraintViolation(t, db, "23503", "order_items_order_id_fk", `INSERT INTO order_items (id, order_id, product_id, product_name, price_snapshot, quantity) VALUES (?, '00000000-0000-0000-0000-000000000000', ?, 'Product', 1, 1)`, uid.New(), fixture.productID)
		expectConstraintViolation(t, db, "23503", "order_items_product_id_fk", `INSERT INTO order_items (id, order_id, product_id, product_name, price_snapshot, quantity) VALUES (?, ?, '00000000-0000-0000-0000-000000000000', 'Product', 1, 1)`, uid.New(), fixture.orderID)
		expectConstraintViolation(t, db, "23514", "order_items_price_snapshot_positive", `INSERT INTO order_items (id, order_id, product_id, product_name, price_snapshot, quantity) VALUES (?, ?, ?, 'Product', 0, 1)`, uid.New(), fixture.orderID, fixture.productID)
		expectConstraintViolation(t, db, "23514", "order_items_quantity_positive", `INSERT INTO order_items (id, order_id, product_id, product_name, price_snapshot, quantity) VALUES (?, ?, ?, 'Product', 1, 0)`, uid.New(), fixture.orderID, fixture.productID)
		if err := db.Exec(`INSERT INTO order_items (id, order_id, product_id, product_name, price_snapshot, quantity) VALUES (?, ?, ?, 'Product', 1, 1)`, uid.New(), fixture.orderID, fixture.productID).Error; err != nil {
			t.Fatalf("insert order item: %v", err)
		}
		expectConstraintViolation(t, db, "23505", "order_items_order_id_product_id_key", `INSERT INTO order_items (id, order_id, product_id, product_name, price_snapshot, quantity) VALUES (?, ?, ?, 'Product Again', 1, 1)`, uid.New(), fixture.orderID, fixture.productID)
	})
	t.Run("orders", func(t *testing.T) {
		fixture.orderID = insertOrder(t, db, fixture, "ORD-001", "client-001", "paid", "")
		expectConstraintViolation(t, db, "23505", "orders_user_id_client_token_key", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address)
			VALUES (?, 'ORD-002', ?, 'client-001', repeat('b', 64), 'paid', 100, 'Buyer', '13800000000', 'Region Detail')`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23514", "orders_client_token_not_blank", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address)
			VALUES (?, 'ORD-003', ?, '   ', repeat('c', 64), 'paid', 100, 'Buyer', '13800000000', 'Region Detail')`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23514", "orders_status_valid", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address)
			VALUES (?, 'ORD-004', ?, 'client-004', repeat('d', 64), 'invalid', 100, 'Buyer', '13800000000', 'Region Detail')`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23514", "orders_shipped_fields_consistent", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address)
			VALUES (?, 'ORD-005', ?, 'client-005', repeat('e', 64), 'shipped', 100, 'Buyer', '13800000000', 'Region Detail')`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23514", "orders_shipped_fields_consistent", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, shipped_by, shipped_at)
			VALUES (?, 'ORD-005B', ?, 'client-005b', repeat('e', 64), 'paid', 100, 'Buyer', '13800000000', 'Region Detail', ?, now())`, uid.New(), fixture.userID, fixture.adminID)
		expectConstraintViolation(t, db, "23514", "orders_completed_at_consistent", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, completed_at)
			VALUES (?, 'ORD-006', ?, 'client-006', repeat('f', 64), 'paid', 100, 'Buyer', '13800000000', 'Region Detail', now())`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23514", "orders_refunded_at_consistent", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, refunded_at)
			VALUES (?, 'ORD-007', ?, 'client-007', repeat('a', 64), 'paid', 100, 'Buyer', '13800000000', 'Region Detail', now())`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23514", "orders_refunded_at_consistent", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, refunded_at)
			VALUES (?, 'ORD-007B', ?, 'client-007b', repeat('a', 64), 'refund_requested', 100, 'Buyer', '13800000000', 'Region Detail', now())`, uid.New(), fixture.userID)
		expectConstraintViolation(t, db, "23514", "orders_refunded_at_consistent", `INSERT INTO orders
			(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address)
			VALUES (?, 'ORD-007C', ?, 'client-007c', repeat('a', 64), 'refunded', 100, 'Buyer', '13800000000', 'Region Detail')`, uid.New(), fixture.userID)

		paidRefundID := insertOrder(t, db, fixture, "ORD-008", "client-008", "paid", "")
		mustExec(t, db, `UPDATE orders SET status = 'refund_requested' WHERE id = ?`, paidRefundID)
		mustExec(t, db, `UPDATE orders SET status = 'refunded', refunded_at = now() WHERE id = ?`, paidRefundID)

		shippedRefundID := insertShippedOrder(t, db, fixture, "ORD-009", "client-009")
		mustExec(t, db, `UPDATE orders SET status = 'refund_requested' WHERE id = ?`, shippedRefundID)
		mustExec(t, db, `UPDATE orders SET status = 'refunded', refunded_at = now() WHERE id = ?`, shippedRefundID)

		completedRefundID := insertCompletedOrder(t, db, fixture, "ORD-010", "client-010")
		mustExec(t, db, `UPDATE orders SET status = 'refund_requested' WHERE id = ?`, completedRefundID)
		mustExec(t, db, `UPDATE orders SET status = 'refunded', refunded_at = now() WHERE id = ?`, completedRefundID)
	})

	t.Run("points ledger", func(t *testing.T) {
		insertLedger(t, db, fixture, "order:1:pay", "order_pay", -100, &fixture.orderID, nil, "", "")
		expectConstraintViolation(t, db, "23505", "uq_order_pay", `INSERT INTO points_ledger
			(id, user_id, order_id, event_key, type, delta, balance_after, remark)
			VALUES (?, ?, ?, 'order:1:pay:duplicate', 'order_pay', -100, 800, '')`, uid.New(), fixture.userID, fixture.orderID)
		expectConstraintViolation(t, db, "23514", "points_ledger_direction_valid", `INSERT INTO points_ledger
			(id, user_id, order_id, event_key, type, delta, balance_after, created_by_admin_id, remark)
			VALUES (?, ?, ?, 'order:1:refund:invalid', 'order_refund', -100, 800, ?, '')`, uid.New(), fixture.userID, fixture.orderID, fixture.adminID)

		insertLedger(t, db, fixture, "order:1:refund", "order_refund", 100, &fixture.orderID, &fixture.adminID, "", "")
		insertLedger(t, db, fixture, "signup:2", "signup_bonus", 100, nil, nil, "", "")
		expectConstraintViolation(t, db, "23505", "uq_signup_bonus_user", `INSERT INTO points_ledger
			(id, user_id, event_key, type, delta, balance_after, remark)
			VALUES (?, ?, 'signup:2:duplicate', 'signup_bonus', 100, 200, '')`, uid.New(), fixture.userID)
		insertLedger(t, db, fixture, "admin:1:adjust", "admin_adjust", -50, nil, &fixture.adminID, "manual correction", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
		expectConstraintViolation(t, db, "23514", "points_ledger_admin_remark_not_blank", `INSERT INTO points_ledger
			(id, user_id, event_key, type, delta, balance_after, created_by_admin_id, remark)
			VALUES (?, ?, 'admin:1:missing-remark', 'admin_adjust', 50, 150, ?, '')`, uid.New(), fixture.userID, fixture.adminID)
	})

	t.Run("audit log append only", func(t *testing.T) {
		var id uid.ID
		if err := db.Raw(`INSERT INTO audit_logs
			(id, actor_admin_id, actor_role, action, target_type, target_id, result, trace_id)
			VALUES (?, ?, 'super_admin', 'product.update', 'product', ?, 'success', 'trace-1') RETURNING id`, uid.New(), fixture.adminID, fixture.productID).Row().Scan(&id); err != nil {
			t.Fatalf("insert audit log: %v", err)
		}
		expectPostgresError(t, db, "P0001", "", `UPDATE audit_logs SET action = 'product.delete' WHERE id = ?`, id)
		expectPostgresError(t, db, "P0001", "", `DELETE FROM audit_logs WHERE id = ?`, id)
		expectPostgresError(t, db, "P0001", "", `TRUNCATE audit_logs`)
		var count int64
		if err := db.Raw(`SELECT count(*) FROM audit_logs WHERE id = ?`, id).Scan(&count).Error; err != nil {
			t.Fatalf("check audit log after rejected truncate: %v", err)
		}
		if count != 1 {
			t.Fatalf("audit log count after rejected truncate = %d, want 1", count)
		}
	})
}

func TestRepositoryModelMappings(t *testing.T) {
	modelCases := []struct {
		name  string
		model any
		table string
	}{
		{"Role", Role{}, "roles"},
		{"Permission", Permission{}, "permissions"},
		{"AdminUser", AdminUser{}, "admin_users"},
		{"User", User{}, "users"},
		{"Product", Product{}, "products"},
		{"Address", Address{}, "user_addresses"},
		{"CartItem", CartItem{}, "cart_items"},
		{"Order", Order{}, "orders"},
		{"OrderItem", OrderItem{}, "order_items"},
		{"PointsLedger", PointsLedger{}, "points_ledger"},
		{"AuditLog", AuditLog{}, "audit_logs"},
	}
	for _, testCase := range modelCases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := schema.Parse(testCase.model, &sync.Map{}, schema.NamingStrategy{})
			if err != nil {
				t.Fatalf("parse model: %v", err)
			}
			if parsed.Table != testCase.table {
				t.Fatalf("table = %q, want %q", parsed.Table, testCase.table)
			}
		})
	}

	assertModelFieldType(t, User{}, "PointsBalance", reflect.TypeOf(int64(0)))
	assertModelFieldType(t, Product{}, "PricePoints", reflect.TypeOf(int64(0)))
	assertModelFieldType(t, Product{}, "Stock", reflect.TypeOf(int32(0)))
	assertModelFieldType(t, Address{}, "Version", reflect.TypeOf(int64(0)))
	assertModelFieldType(t, CartItem{}, "Quantity", reflect.TypeOf(int32(0)))
	assertModelFieldType(t, Order{}, "TotalPoints", reflect.TypeOf(int64(0)))
	assertModelFieldType(t, OrderItem{}, "PriceSnapshot", reflect.TypeOf(int64(0)))
	assertModelFieldType(t, OrderItem{}, "Quantity", reflect.TypeOf(int32(0)))
	assertModelFieldType(t, PointsLedger{}, "Delta", reflect.TypeOf(int64(0)))
	assertModelFieldType(t, PointsLedger{}, "BalanceAfter", reflect.TypeOf(int64(0)))
}

func assertModelFieldType(t *testing.T, model any, name string, want reflect.Type) {
	t.Helper()
	parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse model %T: %v", model, err)
	}
	field := parsed.LookUpField(name)
	if field == nil {
		t.Fatalf("field %s is missing from %T", name, model)
	}
	if field.FieldType != want {
		t.Fatalf("field %s type = %s, want %s", name, field.FieldType, want)
	}
}

type fixtureIDs struct {
	adminID   uid.ID
	userID    uid.ID
	productID uid.ID
	orderID   uid.ID
}

func createFixture(t *testing.T, db *gorm.DB) fixtureIDs {
	t.Helper()
	var fixture fixtureIDs
	if err := db.Exec(`INSERT INTO roles (id, name, remark) VALUES (?, 'fixture', '')`, uid.New()).Error; err != nil {
		t.Fatalf("insert fixture role: %v", err)
	}
	var roleID uid.ID
	if err := db.Raw(`SELECT id FROM roles WHERE name = 'fixture'`).Row().Scan(&roleID); err != nil {
		t.Fatalf("read fixture role: %v", err)
	}
	if err := db.Exec(`INSERT INTO admin_users (id, username, password_hash, role_id) VALUES (?, 'fixture-admin', 'not-a-real-password-hash', ?)`, uid.New(), roleID).Error; err != nil {
		t.Fatalf("insert fixture admin: %v", err)
	}
	if err := db.Raw(`SELECT id FROM admin_users WHERE username = 'fixture-admin'`).Row().Scan(&fixture.adminID); err != nil {
		t.Fatalf("read fixture admin: %v", err)
	}
	if err := db.Exec(`INSERT INTO users (id, openid, nickname) VALUES (?, 'fixture-openid', 'Fixture')`, uid.New()).Error; err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	if err := db.Raw(`SELECT id FROM users WHERE openid = 'fixture-openid'`).Row().Scan(&fixture.userID); err != nil {
		t.Fatalf("read fixture user: %v", err)
	}
	if err := db.Exec(`INSERT INTO products (id, name, price_points, stock) VALUES (?, 'Fixture Product', 100, 10)`, uid.New()).Error; err != nil {
		t.Fatalf("insert fixture product: %v", err)
	}
	if err := db.Raw(`SELECT id FROM products WHERE name = 'Fixture Product'`).Row().Scan(&fixture.productID); err != nil {
		t.Fatalf("read fixture product: %v", err)
	}
	return fixture
}

func insertOrder(t *testing.T, db *gorm.DB, fixture fixtureIDs, orderNo, clientToken, status, suffix string) uid.ID {
	t.Helper()
	query := `INSERT INTO orders
		(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address)
		VALUES (?, ?, ?, ?, repeat('a', 64), ?, 100, 'Buyer', '13800000000', 'Region Detail')`
	if err := db.Exec(query, uid.New(), orderNo, fixture.userID, clientToken, status).Error; err != nil {
		t.Fatalf("insert order %s%s: %v", orderNo, suffix, err)
	}
	if err := db.Raw(`SELECT id FROM orders WHERE order_no = ?`, orderNo).Row().Scan(&fixture.orderID); err != nil {
		t.Fatalf("read order %s: %v", orderNo, err)
	}
	return fixture.orderID
}

func insertShippedOrder(t *testing.T, db *gorm.DB, fixture fixtureIDs, orderNo, clientToken string) uid.ID {
	t.Helper()
	var id uid.ID
	if err := db.Raw(`INSERT INTO orders
		(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, shipped_by, shipped_at)
		VALUES (?, ?, ?, ?, repeat('a', 64), 'shipped', 100, 'Buyer', '13800000000', 'Region Detail', ?, now())
		RETURNING id`, uid.New(), orderNo, fixture.userID, clientToken, fixture.adminID).Row().Scan(&id); err != nil {
		t.Fatalf("insert shipped order %s: %v", orderNo, err)
	}
	return id
}

func insertCompletedOrder(t *testing.T, db *gorm.DB, fixture fixtureIDs, orderNo, clientToken string) uid.ID {
	t.Helper()
	var id uid.ID
	if err := db.Raw(`INSERT INTO orders
		(id, order_no, user_id, client_token, request_hash, status, total_points, receiver, phone, address, shipped_by, shipped_at, completed_at)
		VALUES (?, ?, ?, ?, repeat('a', 64), 'completed', 100, 'Buyer', '13800000000', 'Region Detail', ?, now(), now())
		RETURNING id`, uid.New(), orderNo, fixture.userID, clientToken, fixture.adminID).Row().Scan(&id); err != nil {
		t.Fatalf("insert completed order %s: %v", orderNo, err)
	}
	return id
}

func insertLedger(t *testing.T, db *gorm.DB, fixture fixtureIDs, eventKey, ledgerType string, delta int64, orderID *uid.ID, adminID *uid.ID, remark, requestHash string) {
	t.Helper()
	query := `INSERT INTO points_ledger
		(id, user_id, order_id, event_key, request_hash, type, delta, balance_after, created_by_admin_id, remark)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, 1000, ?, ?)`
	if err := db.Exec(query, uid.New(), fixture.userID, orderID, eventKey, requestHash, ledgerType, delta, adminID, remark).Error; err != nil {
		t.Fatalf("insert ledger %s: %v", eventKey, err)
	}
}

func assertSeed(t *testing.T, db *gorm.DB) {
	t.Helper()
	checks := []struct {
		name  string
		query string
		want  int64
	}{
		{"roles", `SELECT count(*) FROM roles`, 2},
		{"permissions", `SELECT count(*) FROM permissions`, 10},
		{"role permissions", `SELECT count(*) FROM role_permissions`, 17},
		{"admin users", `SELECT count(*) FROM admin_users`, 0},
	}
	for _, check := range checks {
		var got int64
		if err := db.Raw(check.query).Scan(&got).Error; err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if got != check.want {
			t.Errorf("count %s = %d, want %d", check.name, got, check.want)
		}
	}
}

func expectConstraintViolation(t *testing.T, db *gorm.DB, sqlState, constraint, query string, args ...any) {
	t.Helper()
	expectPostgresError(t, db, sqlState, constraint, query, args...)
}

func expectPostgresError(t *testing.T, db *gorm.DB, sqlState, constraint, query string, args ...any) {
	t.Helper()
	err := db.Exec(query, args...).Error
	if err == nil {
		t.Fatalf("expected PostgreSQL error %s for %s", sqlState, query)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected PostgreSQL error for %s, got %T: %v", query, err, err)
	}
	if pgErr.Code != sqlState {
		t.Fatalf("PostgreSQL error code for %s = %s, want %s", query, pgErr.Code, sqlState)
	}
	if constraint != "" && pgErr.ConstraintName != constraint {
		t.Fatalf("PostgreSQL constraint for %s = %q, want %q", query, pgErr.ConstraintName, constraint)
	}
}

func mustExec(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("execute %s: %v", query, err)
	}
}

func openMigrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	cleanup := func() {}
	if dsn == "" {
		var port string
		var container string
		var err error
		container, port, cleanup, err = startPostgresContainer()
		if err != nil {
			t.Fatalf("start PostgreSQL through Docker: %v", err)
		}
		t.Logf("migration test PostgreSQL container=%s port=%s", container, port)
		dsn = fmt.Sprintf("host=127.0.0.1 port=%s user=postgres password=test dbname=shop_mall_test sslmode=disable", port)
	}

	var db *gorm.DB
	var err error
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			if sqlDB, dbErr := db.DB(); dbErr == nil && db.Exec("SELECT 1").Error == nil {
				t.Cleanup(func() {
					_ = sqlDB.Close()
					cleanup()
				})
				return db
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	cleanup()
	t.Fatalf("PostgreSQL did not become ready within 30 seconds: %v", err)
	return nil
}

func startPostgresContainer() (string, string, func(), error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", "", func() {}, err
	}
	container := fmt.Sprintf("shop-mall-migration-test-%d", time.Now().UnixNano())
	cmd := exec.Command("docker", "run", "--rm", "--name", container,
		"-e", "POSTGRES_PASSWORD=test", "-e", "POSTGRES_DB=shop_mall_test",
		"-p", "127.0.0.1::5432", "-d", "postgres:16-alpine")
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", "", func() {}, fmt.Errorf("docker run failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-fv", container).Run()
	}
	output, err := exec.Command("docker", "port", container, "5432/tcp").CombinedOutput()
	if err != nil {
		cleanup()
		return "", "", func() {}, fmt.Errorf("docker port failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	portIndex := strings.LastIndex(line, ":")
	if portIndex < 0 || portIndex == len(line)-1 {
		cleanup()
		return "", "", func() {}, fmt.Errorf("unexpected docker port output %q", line)
	}
	port := line[portIndex+1:]
	if _, err := strconv.Atoi(port); err != nil {
		cleanup()
		return "", "", func() {}, fmt.Errorf("invalid PostgreSQL port %q: %w", port, err)
	}
	return container, port, cleanup, nil
}

func applyMigration(t *testing.T, db *gorm.DB, name string) {
	t.Helper()
	if err := db.Exec(readMigration(t, name)).Error; err != nil {
		t.Fatalf("apply migration %s: %v", name, err)
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(backendRoot(t), "migrations", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(contents)
}

func backendRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
