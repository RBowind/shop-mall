package points_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	apppoints "shop-mall/backend/internal/application/points"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/tests/integration"

	"gorm.io/gorm"
)

type pointFixture struct {
	db      *gorm.DB
	usecase *apppoints.AdjustPointsUsecase
	ledger  *payment.Repository
	adminID uid.ID
}

func newPointFixture(t *testing.T) *pointFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO admin_users (id, username, password_hash, role_id) VALUES (?, 'points-admin', 'x', (SELECT id FROM roles WHERE name = 'super_admin'))`,
		uid.New()).Error; err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	var adminID uid.ID
	if err := db.Raw(`SELECT id FROM admin_users WHERE username = 'points-admin'`).Row().Scan(&adminID); err != nil {
		t.Fatalf("read admin id: %v", err)
	}
	ledger, err := payment.NewRepository(db)
	if err != nil {
		t.Fatalf("new payment repository: %v", err)
	}
	usecase, err := apppoints.NewAdjustPointsUsecase(apppoints.AdjustPointsUsecaseDeps{
		DB:     db,
		Ledger: ledger,
		Now:    func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("new points usecase: %v", err)
	}
	return &pointFixture{db: db, usecase: usecase, ledger: ledger, adminID: adminID}
}

func (f *pointFixture) createUser(t *testing.T, openid string, points int64) uid.ID {
	t.Helper()
	if err := f.db.Exec(`INSERT INTO users (id, openid, points_balance) VALUES (?, ?, ?)`, uid.New(), openid, points).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var id uid.ID
	if err := f.db.Raw(`SELECT id FROM users WHERE openid = ?`, openid).Row().Scan(&id); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	return id
}

func (f *pointFixture) balance(t *testing.T, userID uid.ID) int64 {
	t.Helper()
	var value int64
	if err := f.db.Raw(`SELECT points_balance FROM users WHERE id = ?`, userID).Scan(&value).Error; err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return value
}

func (f *pointFixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := f.db.Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count query: %v", err)
	}
	return count
}

func TestPointsAdjustAppliesDeltaAndWritesLedgerAndAudit(t *testing.T) {
	fix := newPointFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-points-add", 100)
	entry, err := fix.usecase.Execute(ctx, fix.adminID, "key-add", apppoints.PointsAdjustment{
		UserID: userID, Delta: 50, Remark: "活动奖励",
	})
	if err != nil {
		t.Fatalf("adjust points: %v", err)
	}
	if entry.Delta != 50 || entry.BalanceAfter != 150 {
		t.Fatalf("entry delta=%d balance_after=%d, want 50/150", entry.Delta, entry.BalanceAfter)
	}
	if entry.Type != database.LedgerTypeAdminAdjust {
		t.Fatalf("entry type = %s, want admin_adjust", entry.Type)
	}
	if got := fix.balance(t, userID); got != 150 {
		t.Fatalf("balance = %d, want 150", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE event_key = ?`, payment.AdminAdjustEventKey(fix.adminID, "key-add")); got != 1 {
		t.Fatalf("ledger rows = %d, want 1", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'points.adjust' AND result = 'success'`); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
	var actorRole string
	if err := fix.db.Raw(`SELECT actor_role FROM audit_logs WHERE action = 'points.adjust'`).Scan(&actorRole).Error; err != nil {
		t.Fatalf("read audit actor_role: %v", err)
	}
	if actorRole == "" {
		t.Fatal("audit actor_role is blank")
	}
}

func TestPointsAdjustNegativeDeltaDeductsPoints(t *testing.T) {
	fix := newPointFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-points-deduct", 300)
	entry, err := fix.usecase.Execute(ctx, fix.adminID, "key-deduct", apppoints.PointsAdjustment{
		UserID: userID, Delta: -120, Remark: "违规扣减",
	})
	if err != nil {
		t.Fatalf("adjust points: %v", err)
	}
	if entry.Delta != -120 || entry.BalanceAfter != 180 {
		t.Fatalf("entry delta=%d balance_after=%d, want -120/180", entry.Delta, entry.BalanceAfter)
	}
	if got := fix.balance(t, userID); got != 180 {
		t.Fatalf("balance = %d, want 180", got)
	}
}

func TestPointsAdjustReplayReturnsOriginalEntryWithoutDoubleApply(t *testing.T) {
	fix := newPointFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-points-replay", 100)
	req := apppoints.PointsAdjustment{UserID: userID, Delta: 40, Remark: "补发"}
	first, err := fix.usecase.Execute(ctx, fix.adminID, "key-replay", req)
	if err != nil {
		t.Fatalf("first adjust: %v", err)
	}
	replayed, err := fix.usecase.Execute(ctx, fix.adminID, "key-replay", req)
	if err != nil {
		t.Fatalf("replay adjust: %v", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("replay id = %d, want %d", replayed.ID, first.ID)
	}
	if got := fix.balance(t, userID); got != 140 {
		t.Fatalf("balance = %d, want 140 (replay must not double-apply)", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE user_id = ?`, userID); got != 1 {
		t.Fatalf("ledger rows = %d, want 1", got)
	}
}

func TestPointsAdjustSameKeyDifferentBodyIsConflict(t *testing.T) {
	fix := newPointFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-points-conflict", 100)
	if _, err := fix.usecase.Execute(ctx, fix.adminID, "key-conflict", apppoints.PointsAdjustment{
		UserID: userID, Delta: 10, Remark: "第一次",
	}); err != nil {
		t.Fatalf("first adjust: %v", err)
	}
	_, err := fix.usecase.Execute(ctx, fix.adminID, "key-conflict", apppoints.PointsAdjustment{
		UserID: userID, Delta: 20, Remark: "改过的",
	})
	if !errors.Is(err, apppoints.ErrIdempotencyConflict) {
		t.Fatalf("replay error = %v, want ErrIdempotencyConflict", err)
	}
	if got := fix.balance(t, userID); got != 110 {
		t.Fatalf("balance = %d, want 110 (conflict must not apply)", got)
	}
}

func TestPointsAdjustInsufficientBalanceLeavesStateUntouched(t *testing.T) {
	fix := newPointFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-points-insufficient", 30)
	_, err := fix.usecase.Execute(ctx, fix.adminID, "key-insufficient", apppoints.PointsAdjustment{
		UserID: userID, Delta: -100, Remark: "超额扣减",
	})
	if !errors.Is(err, apppoints.ErrInsufficientPoints) {
		t.Fatalf("adjust error = %v, want ErrInsufficientPoints", err)
	}
	if got := fix.balance(t, userID); got != 30 {
		t.Fatalf("balance = %d, want 30 untouched", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM points_ledger WHERE user_id = ?`, userID); got != 0 {
		t.Fatalf("ledger rows = %d, want 0", got)
	}
	if got := fix.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'points.adjust' AND result = 'failure'`); got != 1 {
		t.Fatalf("failure audit rows = %d, want 1", got)
	}
}

func TestPointsAdjustRejectsInvalidAdjustment(t *testing.T) {
	fix := newPointFixture(t)
	ctx := context.Background()
	userID := fix.createUser(t, "openid-points-invalid", 100)
	if _, err := fix.usecase.Execute(ctx, fix.adminID, "key-zero", apppoints.PointsAdjustment{
		UserID: userID, Delta: 0, Remark: "零增量",
	}); !errors.Is(err, apppoints.ErrInvalidAdjustment) {
		t.Fatalf("zero delta error = %v, want ErrInvalidAdjustment", err)
	}
	if _, err := fix.usecase.Execute(ctx, fix.adminID, "key-blank", apppoints.PointsAdjustment{
		UserID: userID, Delta: 10, Remark: "   ",
	}); !errors.Is(err, apppoints.ErrInvalidAdjustment) {
		t.Fatalf("blank remark error = %v, want ErrInvalidAdjustment", err)
	}
	if _, err := fix.usecase.Execute(ctx, fix.adminID, "key-bad-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", apppoints.PointsAdjustment{
		UserID: userID, Delta: 10, Remark: "valid",
	}); !errors.Is(err, apppoints.ErrInvalidIdempotencyKey) {
		t.Fatalf("long key error = %v, want ErrInvalidIdempotencyKey", err)
	}
}
