package user_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/user"
	"shop-mall/backend/tests/integration"

	"gorm.io/gorm"
)

type userFixture struct {
	db  *gorm.DB
	svc *user.Service
}

func newUserFixture(t *testing.T) *userFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	svc, err := user.NewService(user.ServiceDeps{
		DB: db,
	})
	if err != nil {
		t.Fatalf("new user service: %v", err)
	}
	return &userFixture{db: db, svc: svc}
}

func TestUserServiceUpsertByOpenIDIsIdempotent(t *testing.T) {
	fix := newUserFixture(t)
	ctx := context.Background()
	first, err := fix.svc.UpsertByOpenID(ctx, "openid-upsert", "union-upsert", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if uid.IsZero(first.ID) {
		t.Fatal("first upsert returned zero id")
	}
	second, err := fix.svc.UpsertByOpenID(ctx, "openid-upsert", "union-upsert", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("upsert returned different ids: %d vs %d", first.ID, second.ID)
	}
	var count int64
	if err := fix.db.Raw(`SELECT count(*) FROM users WHERE openid = ?`, "openid-upsert").Scan(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("user count after re-upsert = %d, want 1", count)
	}
}

func TestUserServiceSignupBonusIsExactlyOnce(t *testing.T) {
	fix := newUserFixture(t)
	ctx := context.Background()
	first, err := fix.svc.UpsertByOpenID(ctx, "openid-bonus", "union-bonus", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if first.PointsBalance != 100 {
		t.Fatalf("first upsert points balance = %d, want 100", first.PointsBalance)
	}
	second, err := fix.svc.UpsertByOpenID(ctx, "openid-bonus", "union-bonus", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if second.PointsBalance != first.PointsBalance {
		t.Fatalf("re-upsert changed balance: %d vs %d", second.PointsBalance, first.PointsBalance)
	}
	var bonusCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'signup_bonus'`, second.ID).Scan(&bonusCount).Error; err != nil {
		t.Fatalf("count signup bonus: %v", err)
	}
	if bonusCount != 1 {
		t.Fatalf("signup bonus count = %d, want 1", bonusCount)
	}
}

func TestUserServiceExistingZeroBalanceUserDoesNotGetPhantomBonus(t *testing.T) {
	fix := newUserFixture(t)
	ctx := context.Background()
	first, err := fix.svc.UpsertByOpenID(ctx, "openid-zero-balance", "union-zero-balance", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.PointsBalance != 100 {
		t.Fatalf("first upsert balance = %d, want 100", first.PointsBalance)
	}
	// Simulate an existing buyer who spent their entire signup bonus.
	if err := fix.db.Exec(`UPDATE users SET points_balance = 0 WHERE id = ?`, first.ID).Error; err != nil {
		t.Fatalf("spend bonus: %v", err)
	}
	second, err := fix.svc.UpsertByOpenID(ctx, "openid-zero-balance", "union-zero-balance", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if second.PointsBalance != 0 {
		t.Fatalf("re-login reported phantom balance = %d, want 0", second.PointsBalance)
	}
	var dbBalance int64
	if err := fix.db.Raw(`SELECT points_balance FROM users WHERE id = ?`, first.ID).Scan(&dbBalance).Error; err != nil {
		t.Fatalf("read db balance: %v", err)
	}
	if dbBalance != 0 {
		t.Fatalf("db balance = %d, want 0", dbBalance)
	}
	var bonusCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'signup_bonus'`, first.ID).Scan(&bonusCount).Error; err != nil {
		t.Fatalf("count signup bonus: %v", err)
	}
	if bonusCount != 1 {
		t.Fatalf("signup bonus count = %d, want 1", bonusCount)
	}
}

func TestUserServiceConcurrentUpsertCreatesExactlyOneUser(t *testing.T) {
	fix := newUserFixture(t)
	const goroutines = 12
	var wg sync.WaitGroup
	wg.Add(goroutines)
	results := make([]user.User, goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx], errs[idx] = fix.svc.UpsertByOpenID(context.Background(), "openid-race-svc", "union-race-svc", 50, nil, time.Now())
		}(i)
	}
	close(start)
	wg.Wait()
	var firstID uid.ID
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d error: %v", i, err)
		}
		if uid.IsZero(firstID) {
			firstID = results[i].ID
			continue
		}
		if results[i].ID != firstID {
			t.Fatalf("goroutine %d returned id %d, want %d", i, results[i].ID, firstID)
		}
	}
	var count int64
	if err := fix.db.Raw(`SELECT count(*) FROM users WHERE openid = ?`, "openid-race-svc").Scan(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("user count after race = %d, want 1", count)
	}
	var bonusCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'signup_bonus'`, firstID).Scan(&bonusCount).Error; err != nil {
		t.Fatalf("count signup bonus after race: %v", err)
	}
	if bonusCount != 1 {
		t.Fatalf("signup bonus count after race = %d, want 1", bonusCount)
	}
}

func TestUserServiceRejectsBlankOpenID(t *testing.T) {
	fix := newUserFixture(t)
	if _, err := fix.svc.UpsertByOpenID(context.Background(), "   ", "", 0, nil, time.Now()); err == nil {
		t.Fatal("expected error for blank openid")
	}
	if _, err := fix.svc.UpsertByOpenID(context.Background(), "openid-negative-bonus", "", -1, nil, time.Now()); err == nil {
		t.Fatal("expected error for negative signup bonus")
	}
	var nilSvc *user.Service
	if _, err := nilSvc.UpsertByOpenID(context.Background(), "openid-no-svc", "", 0, nil, time.Now()); !errors.Is(err, user.ErrServiceUnavailable) {
		t.Fatalf("expected ErrServiceUnavailable for nil service: %v", err)
	}
}

func TestUserServiceGetByIDReturnsProfileAndMissingIsNotFound(t *testing.T) {
	fix := newUserFixture(t)
	ctx := context.Background()
	created, err := fix.svc.UpsertByOpenID(ctx, "openid-profile", "union-profile", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("upsert profile user: %v", err)
	}

	got, err := fix.svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ID != created.ID || got.OpenID != "openid-profile" || got.PointsBalance != 100 {
		t.Fatalf("profile mismatch: %+v", got)
	}

	if _, err := fix.svc.GetByID(ctx, uid.New()); !errors.Is(err, user.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound for missing id, got %v", err)
	}
	if _, err := fix.svc.GetByID(ctx, uid.ID{}); err == nil {
		t.Fatal("expected error for zero id")
	}
}

func TestUserServiceUpdateProfilePersistsAndReflects(t *testing.T) {
	fix := newUserFixture(t)
	ctx := context.Background()
	created, err := fix.svc.UpsertByOpenID(ctx, "openid-update", "", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("upsert profile user: %v", err)
	}
	nickname := "刀客甲"
	avatarURL := "https://cdn.example.test/a.png"

	updated, err := fix.svc.UpdateProfile(ctx, created.ID, &nickname, &avatarURL)
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated.Nickname != "刀客甲" || updated.AvatarURL != "https://cdn.example.test/a.png" {
		t.Fatalf("updated profile mismatch: %+v", updated)
	}
	if updated.PointsBalance != created.PointsBalance {
		t.Fatalf("update changed points: %d != %d", updated.PointsBalance, created.PointsBalance)
	}

	reloaded, err := fix.svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if reloaded.Nickname != "刀客甲" || reloaded.AvatarURL != "https://cdn.example.test/a.png" {
		t.Fatalf("reloaded profile mismatch: %+v", reloaded)
	}

	ghost := "ghost"
	if _, err := fix.svc.UpdateProfile(ctx, uid.New(), &ghost, nil); !errors.Is(err, user.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound updating a missing user, got %v", err)
	}
}

// TestUserServiceUpdateProfileWritesOnlyPresentFields is the regression test
// for the partial-update data-loss defect: a PATCH that sets only one field
// must leave the absent field untouched.
func TestUserServiceUpdateProfileWritesOnlyPresentFields(t *testing.T) {
	fix := newUserFixture(t)
	ctx := context.Background()
	created, err := fix.svc.UpsertByOpenID(ctx, "openid-partial", "", 100, nil, time.Now())
	if err != nil {
		t.Fatalf("upsert profile user: %v", err)
	}
	avatarURL := "https://cdn.example.test/original.png"
	if _, err := fix.svc.UpdateProfile(ctx, created.ID, nil, &avatarURL); err != nil {
		t.Fatalf("seed avatar_url: %v", err)
	}

	nickname := "刀客甲"
	updated, err := fix.svc.UpdateProfile(ctx, created.ID, &nickname, nil)
	if err != nil {
		t.Fatalf("partial update nickname: %v", err)
	}
	if updated.Nickname != "刀客甲" {
		t.Fatalf("nickname = %q, want 刀客甲", updated.Nickname)
	}
	if updated.AvatarURL != "https://cdn.example.test/original.png" {
		t.Fatalf("avatar_url was wiped by a nickname-only update: %q", updated.AvatarURL)
	}

	avatarURL = "https://cdn.example.test/new.png"
	updated, err = fix.svc.UpdateProfile(ctx, created.ID, nil, &avatarURL)
	if err != nil {
		t.Fatalf("partial update avatar_url: %v", err)
	}
	if updated.AvatarURL != "https://cdn.example.test/new.png" {
		t.Fatalf("avatar_url = %q, want new value", updated.AvatarURL)
	}
	if updated.Nickname != "刀客甲" {
		t.Fatalf("nickname was wiped by an avatar_url-only update: %q", updated.Nickname)
	}
}
