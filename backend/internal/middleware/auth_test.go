package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/config"

	"github.com/gin-gonic/gin"
)

func TestJWTServicesRejectEachOtherAudience(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	buyer := config.JWTConfig{
		Issuer:    "buyer-issuer",
		Audience:  "buyer-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-kid",
		Keys:      map[string][]byte{"buyer-kid": []byte("buyer-test-key-with-at-least-32-bytes")},
	}
	admin := config.JWTConfig{
		Issuer:    "admin-issuer",
		Audience:  "admin-audience",
		TTL:       30 * time.Minute,
		ActiveKID: "admin-kid",
		Keys:      map[string][]byte{"admin-kid": []byte("admin-test-key-with-at-least-32-bytes")},
	}

	buyerService, err := NewJWTService(buyer)
	if err != nil {
		t.Fatalf("new buyer JWT service: %v", err)
	}
	adminService, err := NewJWTService(admin)
	if err != nil {
		t.Fatalf("new admin JWT service: %v", err)
	}

	token, err := buyerService.Issue("buyer-1", 1, now)
	if err != nil {
		t.Fatalf("issue buyer token: %v", err)
	}
	if _, err := adminService.Parse(token, now); err == nil {
		t.Fatal("admin JWT service accepted a buyer token")
	}
}

func TestJWTServiceRejectsTokenBeyondConfiguredTTL(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	cfg := config.JWTConfig{
		Issuer:    "buyer-issuer",
		Audience:  "buyer-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-kid",
		Keys:      map[string][]byte{"buyer-kid": []byte("buyer-test-key-with-at-least-32-bytes")},
	}
	service, err := NewJWTService(cfg)
	if err != nil {
		t.Fatalf("new JWT service: %v", err)
	}

	token, err := service.IssueAt("buyer-1", 1, now, 2*time.Hour)
	if err == nil {
		t.Fatal("IssueAt accepted a token longer than configured TTL")
	}
	if token != "" {
		t.Fatalf("IssueAt returned a token after rejecting TTL: %q", token)
	}
}

// stubAdminStateResolver is a deterministic AdminStateResolver for middleware
// tests. It avoids a database dependency while exercising the freshness and
// enabled enforcement in AdminAuthWithClock.
type stubAdminStateResolver struct {
	state AdminState
}

func (s stubAdminStateResolver) AdminState(_ context.Context, _ uid.ID) (AdminState, error) {
	return s.state, nil
}

func TestAdminAuthEnforcesTokenVersionAndEnabledState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	cfg := config.JWTConfig{
		Issuer:    "admin-issuer",
		Audience:  "admin-audience",
		TTL:       30 * time.Minute,
		ActiveKID: "admin-kid",
		Keys:      map[string][]byte{"admin-kid": []byte("admin-test-key-material-with-at-least-32-bytes")},
	}
	service, err := NewJWTService(cfg)
	if err != nil {
		t.Fatalf("new JWT service: %v", err)
	}
	const cookieName = "admin_access"
	token, err := service.Issue("admin:"+uid.New().String(), 1, now)
	if err != nil {
		t.Fatalf("issue admin token: %v", err)
	}

	run := func(t *testing.T, state AdminState, wantStatus int) {
		t.Helper()
		router := gin.New()
		reached := false
		router.Use(AdminAuthWithClock(service, cookieName, func() time.Time { return now }, stubAdminStateResolver{state: state}))
		router.GET("/probe", func(c *gin.Context) {
			reached = true
			c.Status(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != wantStatus {
			t.Fatalf("status = %d, want %d (reached=%v)", res.Code, wantStatus, reached)
		}
		if reached != (res.Code == http.StatusOK) {
			t.Fatalf("handler reached = %v, want %v", reached, res.Code == http.StatusOK)
		}
	}

	t.Run("current version and enabled", func(t *testing.T) {
		run(t, AdminState{Enabled: true, TokenVersion: 1}, http.StatusOK)
	})
	t.Run("stale token version", func(t *testing.T) {
		run(t, AdminState{Enabled: true, TokenVersion: 2}, http.StatusUnauthorized)
	})
	t.Run("disabled admin", func(t *testing.T) {
		run(t, AdminState{Enabled: false, TokenVersion: 1}, http.StatusUnauthorized)
	})
	t.Run("subject without admin prefix", func(t *testing.T) {
		// A buyer-shaped subject must never clear the admin middleware even
		// when the resolver would otherwise allow it.
		buyerToken, err := service.Issue("buyer:"+uid.New().String(), 1, now)
		if err != nil {
			t.Fatalf("issue buyer-shaped token: %v", err)
		}
		router := gin.New()
		router.Use(AdminAuthWithClock(service, cookieName, func() time.Time { return now }, stubAdminStateResolver{state: AdminState{Enabled: true, TokenVersion: 1}}))
		router.GET("/probe", func(c *gin.Context) { c.Status(http.StatusOK) })
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: buyerToken})
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("buyer-shaped subject status = %d, want 401", res.Code)
		}
	})
}

type stubBuyerResolver struct {
	exists map[uid.ID]bool
}

func (s stubBuyerResolver) BuyerExists(_ context.Context, buyerID uid.ID) (bool, error) {
	return s.exists[buyerID], nil
}

// TestBuyerAuthWithStateRejectsDanglingSubject: a valid JWT whose subject no
// longer exists in users must be rejected as 401 (not reach handlers with a
// dangling id), mirroring the database-reset scenario that previously made
// cart writes explode as 500 foreign-key errors.
func TestBuyerAuthWithStateRejectsDanglingSubject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.JWTConfig{
		Issuer:    "buyer-issuer",
		Audience:  "buyer-audience",
		TTL:       time.Hour,
		ActiveKID: "buyer-kid",
		Keys:      map[string][]byte{"buyer-kid": []byte("buyer-test-key-with-at-least-32-bytes")},
	}
	service, err := NewJWTService(cfg)
	if err != nil {
		t.Fatalf("new JWT service: %v", err)
	}

	existingID := uid.New()
	r := gin.New()
	r.GET("/protected", BuyerAuthWithState(service, stubBuyerResolver{exists: map[uid.ID]bool{existingID: true}}), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"subject": c.GetString(BuyerSubjectKey)})
	})

	// Existing buyer → request reaches the handler.
	token, err := service.Issue("buyer:"+existingID.String(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("existing buyer status = %d, want 200", w.Code)
	}

	// Dangling subject → 401 before the handler.
	dangling, err := service.Issue("buyer:"+uid.New().String(), 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("issue dangling token: %v", err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req2.Header.Set("Authorization", "Bearer "+dangling)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("dangling subject status = %d, want 401", w2.Code)
	}

	// Nil resolver keeps the legacy signature-only behavior.
	r2 := gin.New()
	r2.GET("/protected", BuyerAuth(service), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"subject": c.GetString(BuyerSubjectKey)})
	})
	req3 := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req3.Header.Set("Authorization", "Bearer "+dangling)
	w3 := httptest.NewRecorder()
	r2.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("legacy nil-resolver status = %d, want 200", w3.Code)
	}
}
