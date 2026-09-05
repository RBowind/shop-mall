package auth_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	applogin "shop-mall/backend/internal/application/auth"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/platform/database"
	signerauth "shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/platform/wechat"
	"shop-mall/backend/tests/integration"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// loginFixture wires the minimum collaborators needed to exercise the buyer
// login usecase: a real PostgreSQL schema, a deterministic WeChat adapter, a
// configured JWT signing service and a captured slog buffer used to assert
// that the discarded session_key never appears in logs.
type loginFixture struct {
	db          *gorm.DB
	jwtService  *signerauth.Signer
	wechat      *wechat.FakeClient
	usecase     *applogin.LoginUsecase
	logBuf      *concurrentBuffer
	now         time.Time
	signupBonus int64
}

func newLoginFixture(t *testing.T, openID string) *loginFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	cfg := config.JWTConfig{
		Issuer:    "buyer-test-issuer",
		Audience:  "buyer-test-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-test-kid",
		Keys: map[string][]byte{
			"buyer-test-kid": []byte("buyer-test-key-material-with-at-least-32-bytes"),
		},
	}
	signer, err := signerauth.NewSigner(cfg)
	if err != nil {
		t.Fatalf("new JWT signer: %v", err)
	}
	logBuf := &concurrentBuffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	wechatClient := wechat.NewFakeClientWithSession(openID, "union-"+openID, "session-key-leaked-on-purpose")
	usecase := applogin.NewLoginUsecase(applogin.LoginUsecaseDeps{
		DB:          db,
		WeChat:      wechatClient,
		Signer:      signer,
		Logger:      logger,
		Now:         func() time.Time { return now },
		SignupBonus: 100,
	})
	return &loginFixture{
		db:          db,
		jwtService:  signer,
		wechat:      wechatClient,
		usecase:     usecase,
		logBuf:      logBuf,
		now:         now,
		signupBonus: 100,
	}
}

func TestLoginUsecaseExecuteOnFirstLoginCreatesUserAndSignupBonus(t *testing.T) {
	fix := newLoginFixture(t, "openid-first-login")

	result, err := fix.usecase.Execute(context.Background(), "code-first")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if result.User.OpenID != "openid-first-login" {
		t.Fatalf("openid = %q, want %q", result.User.OpenID, "openid-first-login")
	}
	if uid.IsZero(result.User.ID) {
		t.Fatal("login result has zero user id")
	}
	if result.User.PointsBalance != int64(fix.signupBonus) {
		t.Fatalf("points balance = %d, want %d", result.User.PointsBalance, fix.signupBonus)
	}
	if result.Token == "" {
		t.Fatal("login result has empty token")
	}
	claims, err := fix.jwtService.Parse(result.Token, fix.now)
	if err != nil {
		t.Fatalf("parse issued token: %v", err)
	}
	if claims.Subject != result.User.Subject {
		t.Fatalf("subject = %q, want %q", claims.Subject, result.User.Subject)
	}

	var bonusCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'signup_bonus'`, result.User.ID).Scan(&bonusCount).Error; err != nil {
		t.Fatalf("count signup bonus: %v", err)
	}
	if bonusCount != 1 {
		t.Fatalf("signup bonus count = %d, want 1", bonusCount)
	}
}

func TestLoginUsecaseReturnsExistingUserWithoutSecondSignupBonus(t *testing.T) {
	fix := newLoginFixture(t, "openid-existing")
	first, err := fix.usecase.Execute(context.Background(), "code-existing-1")
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	second, err := fix.usecase.Execute(context.Background(), "code-existing-2")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if first.User.ID != second.User.ID {
		t.Fatalf("user id drifted: first=%d second=%d", first.User.ID, second.User.ID)
	}
	if second.User.PointsBalance != first.User.PointsBalance {
		t.Fatalf("second login changed balance: first=%d second=%d", first.User.PointsBalance, second.User.PointsBalance)
	}
	var bonusCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'signup_bonus'`, second.User.ID).Scan(&bonusCount).Error; err != nil {
		t.Fatalf("count signup bonus: %v", err)
	}
	if bonusCount != 1 {
		t.Fatalf("signup bonus count after re-login = %d, want 1", bonusCount)
	}
}

func TestLoginUsecaseConcurrentFirstLoginCreatesExactlyOneUser(t *testing.T) {
	fix := newLoginFixture(t, "openid-race")
	const goroutines = 16
	results := make([]applogin.LoginResult, goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx], errs[idx] = fix.usecase.Execute(context.Background(), "code-race")
		}(i)
	}
	close(start)
	wg.Wait()

	var successCount int
	var firstID = uid.ID{}
	var firstSubject string
	for i, err := range errs {
		if err == nil {
			successCount++
			if uid.IsZero(firstID) {
				firstID = results[i].User.ID
				firstSubject = results[i].User.Subject
			} else if results[i].User.ID != firstID {
				t.Fatalf("concurrent login created multiple users: ids=%d,%d", firstID, results[i].User.ID)
			}
			if results[i].Token == "" {
				t.Fatalf("concurrent login returned empty token at index %d", i)
			}
			if results[i].User.Subject != firstSubject {
				t.Fatalf("concurrent login subjects differ for same user")
			}
			if _, err := fix.jwtService.Parse(results[i].Token, fix.now); err != nil {
				t.Fatalf("concurrent login token invalid at index %d: %v", i, err)
			}
		}
	}
	if successCount != goroutines {
		t.Fatalf("success count = %d, want %d", successCount, goroutines)
	}
	var bonusCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM points_ledger WHERE user_id = ? AND type = 'signup_bonus'`, firstID).Scan(&bonusCount).Error; err != nil {
		t.Fatalf("count signup bonus after race: %v", err)
	}
	if bonusCount != 1 {
		t.Fatalf("signup bonus count after concurrent race = %d, want 1", bonusCount)
	}
	var userCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM users WHERE openid = ?`, "openid-race").Scan(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 1 {
		t.Fatalf("user count = %d, want 1", userCount)
	}
}

func TestLoginUsecaseDoesNotLeakSessionKeyInLogsOrResult(t *testing.T) {
	fix := newLoginFixture(t, "openid-no-leak")
	if _, err := fix.usecase.Execute(context.Background(), "code-no-leak"); err != nil {
		t.Fatalf("login: %v", err)
	}
	logs := fix.logBuf.String()
	for _, secret := range []string{"session-key-leaked-on-purpose", "session_key", "sessionkey"} {
		if strings.Contains(strings.ToLower(logs), strings.ToLower(secret)) {
			t.Fatalf("logs contained session_key fragment %q: %s", secret, logs)
		}
	}
}

func TestLoginUsecasePropagatesWeChatError(t *testing.T) {
	fix := newLoginFixture(t, "openid-wechat-error")
	fix.wechat.Err = errors.New("wechat service unavailable")
	_, err := fix.usecase.Execute(context.Background(), "code-wechat-error")
	if err == nil {
		t.Fatal("expected an error when WeChat returns one")
	}
	if !strings.Contains(err.Error(), "wechat") {
		t.Fatalf("error did not mention wechat: %v", err)
	}
}

func TestLoginUsecaseRejectsBlankCode(t *testing.T) {
	fix := newLoginFixture(t, "openid-blank")
	if _, err := fix.usecase.Execute(context.Background(), " "); err == nil {
		t.Fatal("expected an error for a blank code")
	}
}

func TestLoginUsecaseDoesNotInsertUserWhenSessionLookupFails(t *testing.T) {
	fix := newLoginFixture(t, "openid-failed-lookup")
	fix.wechat.Err = errors.New("wechat returned 40001")
	if _, err := fix.usecase.Execute(context.Background(), "code-failed"); err == nil {
		t.Fatal("expected error when WeChat fails")
	}
	var userCount int64
	if err := fix.db.Raw(`SELECT count(*) FROM users WHERE openid = ?`, "openid-failed-lookup").Scan(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 0 {
		t.Fatalf("user count after failed lookup = %d, want 0", userCount)
	}
}

func TestLoginUsecaseRejectsBlankOpenIDFromAdapter(t *testing.T) {
	fix := newLoginFixture(t, "openid-blank-from-adapter")
	fix.wechat.OpenID = "   "
	if _, err := fix.usecase.Execute(context.Background(), "code-blank-openid"); err == nil {
		t.Fatal("expected error when adapter returns a blank openid")
	}
}

func TestLoginUsecaseSurvivesSerializationRetry(t *testing.T) {
	fix := newLoginFixture(t, "openid-serialization")
	// Inject a one-shot serialization failure to confirm retry behaviour.
	hook := &serializationHook{failOnce: true}
	if err := fix.db.Callback().Create().Before("gorm:create").Register("serialization_failure", hook.Hook); err != nil {
		t.Fatalf("register serialization hook: %v", err)
	}
	result, err := fix.usecase.Execute(context.Background(), "code-serialization")
	if err != nil {
		t.Fatalf("login with serialization retry: %v", err)
	}
	if result.User.OpenID != "openid-serialization" {
		t.Fatalf("unexpected openid after retry: %q", result.User.OpenID)
	}
	if hook.calls == 0 {
		t.Fatal("expected serialization hook to fire at least once")
	}
}

type serializationHook struct {
	failOnce bool
	calls    int
}

func (h *serializationHook) Hook(tx *gorm.DB) {
	h.calls++
	if h.failOnce && h.calls == 1 {
		tx.Error = &pgconn.PgError{Code: "40001"}
	}
}

// concurrentBuffer protects a bytes.Buffer for use from multiple goroutines,
// which the slog handler may invoke concurrently when the buyer login races.
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
