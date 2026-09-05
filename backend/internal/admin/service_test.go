package admin_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/logging"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/platform/wechat"
	"shop-mall/backend/tests/integration"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type adminFixture struct {
	db          *gorm.DB
	service     *admin.Service
	signer      *tokens.Signer
	logBuf      *concurrentBuffer
	logger      *slog.Logger
	now         time.Time
	cookieCfg   config.AdminCookieConfig
	rateLimiter *admin.LoginRateLimiter
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	db := integration.OpenTestDatabase(t)
	if err := database.RunMigrations(context.Background(), db, "../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	logBuf := &concurrentBuffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	svc, err := admin.NewService(admin.ServiceDeps{
		DB:     db,
		Logger: logger,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new admin service: %v", err)
	}
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
	limiter := admin.NewLoginRateLimiterConfig(admin.LoginRateLimiterConfig{
		AccountLimit:  10,
		AccountWindow: time.Minute,
		IPLimit:       5,
		IPWindow:      time.Minute,
	})
	limiter.SetNow(func() time.Time { return now })
	svc.SetRateLimiter(limiter)
	return &adminFixture{
		db:          db,
		service:     svc,
		signer:      signer,
		logBuf:      logBuf,
		logger:      logger,
		now:         now,
		cookieCfg:   config.DefaultAdminCookieConfig(),
		rateLimiter: limiter,
	}
}

func TestBootstrapAdministratorCreatesAndRejectsWeakPassword(t *testing.T) {
	fix := newAdminFixture(t)
	result, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "bootstrap-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if uid.IsZero(result.AdminID) {
		t.Fatal("bootstrap returned zero admin id")
	}
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "bootstrap-admin-2",
		Password:  "password",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err == nil {
		t.Fatal("expected weak password rejection")
	}
	for _, weak := range []string{"short", "alllowercase", "ALLUPPERCASE", "NoDigits!", "nodigits", "NoSpecial1", "12345678", "abcdefgh", "ABCDEFGH"} {
		if err := validateStrongPassword(weak); err == nil {
			t.Fatalf("expected weak password %q to be rejected", weak)
		}
	}
	if err := validateStrongPassword("Sup3rSecret!Pass"); err != nil {
		t.Fatalf("expected strong password to pass: %v", err)
	}
}

func TestBootstrapAdministratorRejectsDuplicateUsername(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "duplicate-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "duplicate-admin",
		Password:  "An0therStr0ng!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err == nil {
		t.Fatal("expected duplicate bootstrap to be rejected")
	}
	var storedHash string
	if err := fix.db.Raw(`SELECT password_hash FROM admin_users WHERE username = ?`, "duplicate-admin").Scan(&storedHash).Error; err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if storedHash == "" || strings.Contains(storedHash, "An0therStr0ng!Pass") {
		t.Fatalf("second bootstrap mutated stored credentials: %q", storedHash)
	}
}

func TestBootstrapAdministratorRejectsUnknownRole(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "ghost-role-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "ghost-role",
		ActorName: "bootstrap",
	}); err == nil {
		t.Fatal("expected unknown role to be rejected")
	}
}

func TestBootstrapAdministratorRejectsBlankUsername(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "   ",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err == nil {
		t.Fatal("expected blank username rejection")
	}
}

func TestBootstrapAdministratorWritesAuditLog(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "audit-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	row := firstAudit(t, fix.db, "admin.bootstrap")
	if row.Result != "success" {
		t.Fatalf("audit result = %q, want success", row.Result)
	}
	if row.ActorAdminID != nil {
		t.Fatalf("bootstrap audit must not have actor_admin_id, got %v", *row.ActorAdminID)
	}
	if row.ActorRole != "system" {
		t.Fatalf("audit actor_role = %q, want system", row.ActorRole)
	}
}

func TestAdminLoginAndLogoutBumpTokenVersion(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "login-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	login, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "login-admin",
		Password: "Sup3rSecret!Pass",
	}, fix.signer)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.Token == "" {
		t.Fatal("login returned empty token")
	}
	claims, err := fix.signer.Parse(login.Token, fix.now)
	if err != nil {
		t.Fatalf("parse login token: %v", err)
	}
	if claims.TokenVersion != login.TokenVersion {
		t.Fatalf("token version mismatch: jwt=%d db=%d", claims.TokenVersion, login.TokenVersion)
	}
	if err := fix.service.Logout(context.Background(), claims.Subject, login.AdminID); err != nil {
		t.Fatalf("logout: %v", err)
	}
	ok, err := fix.service.IsTokenVersionCurrent(context.Background(), login.AdminID, claims.TokenVersion)
	if err != nil {
		t.Fatalf("check token version: %v", err)
	}
	if ok {
		t.Fatal("old token must be invalid after logout")
	}
	if err := fix.db.Exec(`UPDATE admin_users SET enabled = false WHERE id = ?`, login.AdminID).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "login-admin",
		Password: "Sup3rSecret!Pass",
	}, fix.signer); err == nil {
		t.Fatal("disabled admin must not be able to log in")
	}
}

func TestAdminLoginRejectsWrongPassword(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "wrong-pw-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	_, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "wrong-pw-admin",
		Password: "WrongPass!1",
	}, fix.signer)
	if err == nil {
		t.Fatal("expected wrong-password rejection")
	}
	row := firstAudit(t, fix.db, "admin.login")
	if row.Result != "failure" {
		t.Fatalf("audit result = %q, want failure", row.Result)
	}
}

func TestAdminLoginRateLimitIsTwoDimensional(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "account-spray-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// The per-IP budget is 5/minute. Six attempts from one host exhaust the
	// IP dimension regardless of the account dimension.
	for i := 0; i < 5; i++ {
		if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
			Username: "account-spray-admin",
			Password: "WrongPass!1",
			IP:       "10.0.0.10",
		}, fix.signer); err == nil {
			t.Fatalf("expected wrong-password error on attempt %d", i)
		}
	}
	if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "account-spray-admin",
		Password: "WrongPass!1",
		IP:       "10.0.0.10",
	}, fix.signer); err == nil {
		t.Fatal("expected rate-limit rejection once the per-IP budget is exhausted")
	}
	// The account budget is 10/minute. After ten attempts spread across ten
	// hosts the account dimension is exhausted, so a fresh host is locked out
	// too: spraying the account from many hosts cannot bypass the limit.
	for i := 0; i < 9; i++ {
		if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
			Username: "account-spray-admin",
			Password: "WrongPass!1",
			IP:       fmt.Sprintf("10.0.1.%d", i+1),
		}, fix.signer); err == nil {
			t.Fatalf("expected an error on account attempt %d", i)
		}
	}
	if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "account-spray-admin",
		Password: "Sup3rSecret!Pass",
		IP:       "10.0.2.200",
	}, fix.signer); err == nil {
		t.Fatal("expected rate-limit rejection when the per-account budget is exhausted")
	}
}

func TestAdminLoginRateLimitByAccountAndIP(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "rate-limit-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
			Username: "rate-limit-admin",
			Password: "WrongPass!1",
			IP:       "10.0.0.1",
		}, fix.signer); err == nil {
			t.Fatalf("expected wrong-password error on attempt %d", i)
		}
	}
	_, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "rate-limit-admin",
		Password: "WrongPass!1",
		IP:       "10.0.0.1",
	}, fix.signer)
	if err == nil {
		t.Fatal("expected rate-limit rejection")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected rate-limit error, got %v", err)
	}
	if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "rate-limit-admin",
		Password: "Sup3rSecret!Pass",
		IP:       "10.0.0.2",
	}, fix.signer); err != nil {
		t.Fatalf("different IP should not be rate-limited: %v", err)
	}
}

func TestAdminPasswordChangeInvalidatesOldToken(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "pw-change-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	login, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "pw-change-admin",
		Password: "Sup3rSecret!Pass",
	}, fix.signer)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	oldToken := login.Token
	claims, err := fix.signer.Parse(oldToken, fix.now)
	if err != nil {
		t.Fatalf("parse old token: %v", err)
	}
	if _, err := fix.service.ChangePassword(context.Background(), admin.ChangePasswordInput{
		ActorAdminID: login.AdminID,
		ActorRole:    "self",
		Subject:      claims.Subject,
		OldPassword:  "Sup3rSecret!Pass",
		NewPassword:  "Sup3rSecret!NewPass",
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}
	ok, err := fix.service.IsTokenVersionCurrent(context.Background(), login.AdminID, claims.TokenVersion)
	if err != nil {
		t.Fatalf("check token version: %v", err)
	}
	if ok {
		t.Fatal("old token must be invalid after password change")
	}
	if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "pw-change-admin",
		Password: "Sup3rSecret!Pass",
	}, fix.signer); err == nil {
		t.Fatal("old password should be rejected")
	}
	if _, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "pw-change-admin",
		Password: "Sup3rSecret!NewPass",
	}, fix.signer); err != nil {
		t.Fatalf("new password should be accepted: %v", err)
	}
}

func TestPermissionsAreReadFromCurrentRolePerRequest(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "perm-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "operator",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap operator: %v", err)
	}
	operatorID := readAdminID(t, fix.db, "perm-admin")
	perms, err := fix.service.PermissionsForAdmin(context.Background(), operatorID)
	if err != nil {
		t.Fatalf("permissions: %v", err)
	}
	if !containsPermission(perms, "product:read") {
		t.Fatalf("operator permissions missing product:read: %v", perms)
	}
	if containsPermission(perms, "role:manage") {
		t.Fatalf("operator permissions should not include role:manage: %v", perms)
	}
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "super-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap super: %v", err)
	}
	superID := readAdminID(t, fix.db, "super-admin")
	superPerms, err := fix.service.PermissionsForAdmin(context.Background(), superID)
	if err != nil {
		t.Fatalf("super permissions: %v", err)
	}
	if !containsPermission(superPerms, "role:manage") {
		t.Fatalf("super_admin must include role:manage: %v", superPerms)
	}
	if _, err := fix.service.AssignRole(context.Background(), admin.AssignRoleInput{
		ActorAdminID: superID,
		ActorRole:    "super_admin",
		TargetID:     operatorID,
		NewRoleName:  "super_admin",
	}); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	perms, err = fix.service.PermissionsForAdmin(context.Background(), operatorID)
	if err != nil {
		t.Fatalf("permissions after role change: %v", err)
	}
	if !containsPermission(perms, "role:manage") {
		t.Fatalf("operator permissions after promotion missing role:manage: %v", perms)
	}
}

func TestAdminHandlerLoginWithCSRFAndOriginEnforcement(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "handler-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	body := strings.NewReader(`{"username":"handler-admin","password":"Sup3rSecret!Pass"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example.test")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", res.Code, res.Body.String())
	}
	var cookies []*http.Cookie //nolint:staticcheck // SA4010: collected for future per-cookie assertions
	accessFound := false
	csrfFound := false
	for _, raw := range res.Header().Values("Set-Cookie") {
		parsed, parseErr := http.ParseSetCookie(raw)
		if parseErr != nil {
			t.Fatalf("parse cookie: %v", parseErr)
		}
		_ = append(cookies, parsed) //nolint:staticcheck // SA4010: see cookies declaration
		if parsed.Name == fix.cookieCfg.AccessTokenName {
			accessFound = true
		}
		if parsed.Name == fix.cookieCfg.CSRFName {
			csrfFound = true
		}
	}
	if !accessFound {
		t.Fatal("login response did not set access token cookie")
	}
	if !csrfFound {
		t.Fatal("login response did not set CSRF cookie")
	}
	body = strings.NewReader(`{"username":"handler-admin","password":"Sup3rSecret!Pass"}`)
	missingOrigin := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", body)
	missingOrigin.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	router.ServeHTTP(res, missingOrigin)
	if res.Code != http.StatusForbidden {
		t.Fatalf("login without origin = %d, want 403", res.Code)
	}
	body = strings.NewReader(`{"current_password":"x","new_password":"y"}`)
	bogusOrigin := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/password", body)
	bogusOrigin.Header.Set("Content-Type", "application/json")
	bogusOrigin.Header.Set("Origin", "https://evil.example.test")
	res = httptest.NewRecorder()
	router.ServeHTTP(res, bogusOrigin)
	if res.Code != http.StatusForbidden {
		t.Fatalf("password change from bogus origin = %d, want 403", res.Code)
	}
}

func TestAdminHandlerWriteRequiresMatchingCSRF(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "csrf-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	body := strings.NewReader(`{"username":"csrf-admin","password":"Sup3rSecret!Pass"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "https://admin.example.test")
	loginRes := httptest.NewRecorder()
	router.ServeHTTP(loginRes, loginReq)
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginRes.Code)
	}
	var csrfCookie *http.Cookie
	for _, raw := range loginRes.Header().Values("Set-Cookie") {
		parsed, _ := http.ParseSetCookie(raw)
		if parsed.Name == fix.cookieCfg.CSRFName {
			csrfCookie = parsed
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("csrf cookie missing after login")
	}
	writeBodyString := `{"current_password":"Sup3rSecret!Pass","new_password":"Sup3rSecret!NewPass"}`
	writeBody := strings.NewReader(writeBodyString)
	missingCSRF := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/password", writeBody)
	missingCSRF.Header.Set("Content-Type", "application/json")
	missingCSRF.Header.Set("Origin", "https://admin.example.test")
	for _, raw := range loginRes.Header().Values("Set-Cookie") {
		parsed, _ := http.ParseSetCookie(raw)
		if parsed.Name == fix.cookieCfg.AccessTokenName {
			missingCSRF.AddCookie(parsed)
		}
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, missingCSRF)
	if res.Code != http.StatusForbidden {
		t.Fatalf("password change without CSRF = %d, want 403", res.Code)
	}
	matching := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/password", strings.NewReader(writeBodyString))
	matching.Header.Set("Content-Type", "application/json")
	matching.Header.Set("Origin", "https://admin.example.test")
	matching.Header.Set("X-CSRF-Token", csrfCookie.Value)
	for _, raw := range loginRes.Header().Values("Set-Cookie") {
		parsed, _ := http.ParseSetCookie(raw)
		matching.AddCookie(parsed)
	}
	res = httptest.NewRecorder()
	router.ServeHTTP(res, matching)
	if res.Code != http.StatusOK {
		t.Fatalf("password change with CSRF = %d, want 200; body=%s", res.Code, res.Body.String())
	}
}

func TestAdminHandlerRejectsStaleTokenVersion(t *testing.T) {
	fix := newAdminFixture(t)
	if _, err := fix.service.BootstrapAdministrator(context.Background(), admin.BootstrapInput{
		Username:  "stale-admin",
		Password:  "Sup3rSecret!Pass",
		RoleName:  "super_admin",
		ActorName: "bootstrap",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	login, err := fix.service.Authenticate(context.Background(), admin.AuthenticateInput{
		Username: "stale-admin",
		Password: "Sup3rSecret!Pass",
	}, fix.signer)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := fix.service.Logout(context.Background(), login.Subject, login.AdminID); err != nil {
		t.Fatalf("logout: %v", err)
	}
	router := newAdminRouter(fix, []string{"https://admin.example.test"})
	probe := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/me", nil)
	probe.AddCookie(&http.Cookie{Name: fix.cookieCfg.AccessTokenName, Value: login.Token})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, probe)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("stale token status = %d, want 401", res.Code)
	}
}

func newAdminRouter(fix *adminFixture, allowedOrigins []string) http.Handler {
	gin.SetMode(gin.TestMode)
	cfg := config.Config{
		AllowedOrigins: allowedOrigins,
		AdminCookie:    fix.cookieCfg,
		AdminJWT: config.JWTConfig{
			Issuer:    "admin-test-issuer",
			Audience:  "admin-test-audience",
			TTL:       30 * time.Minute,
			ActiveKID: "admin-test-kid",
			Keys: map[string][]byte{
				"admin-test-kid": []byte("admin-test-key-material-with-at-least-32-bytes"),
			},
		},
	}
	router := platformhttp.NewRouter(cfg, fix.db, platformhttp.Dependencies{
		Logger: fix.logger,
		RegisterAdminRoutes: func(group *gin.RouterGroup) {
			handler, err := admin.NewHandler(admin.HandlerDeps{
				Service:     fix.service,
				Signer:      fix.signer,
				RateLimiter: fix.rateLimiter,
				Logger:      fix.logger,
				Cookie:      fix.cookieCfg,
				Now:         func() time.Time { return fix.now },
			})
			if err != nil {
				panic(err)
			}
			admin.RegisterRoutes(group, handler)
		},
	})
	return router
}

func readAdminID(t *testing.T, db *gorm.DB, username string) uid.ID {
	t.Helper()
	var id uid.ID
	err := db.Raw(`SELECT id FROM admin_users WHERE username = ?`, username).Row().Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uid.ID{}
	}
	if err != nil {
		t.Fatalf("read admin id: %v", err)
	}
	return id
}

func containsPermission(perms []string, want string) bool {
	for _, p := range perms {
		if p == want {
			return true
		}
	}
	return false
}

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

func validateStrongPassword(value string) error {
	return admin.ValidatePassword(value)
}

var _ = errors.New
var _ = json.Marshal
var _ = wechat.Session{}
var _ = middleware.JWTClaims{}
var _ = logging.RequestLogger
