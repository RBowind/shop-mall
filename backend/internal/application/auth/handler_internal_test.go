package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestMapLoginError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   int
	}{
		{"blank code", ErrLoginCodeRequired, http.StatusBadRequest, 1001},
		{"wechat lookup failure", fmt.Errorf("wrapped: %w", ErrWeChatLookup), http.StatusUnprocessableEntity, 2002},
		{"empty openid", ErrEmptyOpenID, http.StatusUnprocessableEntity, 2002},
		{"internal failure", errors.New("database down"), http.StatusInternalServerError, 5001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code := mapLoginError(tc.err)
			if status != tc.wantStatus || code != tc.wantCode {
				t.Fatalf("mapLoginError(%v) = (%d, %d), want (%d, %d)", tc.err, status, code, tc.wantStatus, tc.wantCode)
			}
		})
	}
}

// TestLoginHandlerRejectsBadPayloads proves the handler rejects a non-JSON
// body, a blank code and an over-long code before the usecase is reached, so
// no database is required.
func TestLoginHandlerRejectsBadPayloads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, err := NewLoginHandler(LoginHandlerDeps{Usecase: &LoginUsecase{}})
	if err != nil {
		t.Fatalf("new login handler: %v", err)
	}

	cases := []struct {
		name string
		body string
	}{
		{"malformed JSON", "{not-json"},
		{"blank code", `{"code":"   "}`},
		{"missing code", `{}`},
		{"over-long code", `{"code":"` + strings.Repeat("c", 513) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := performLoginRequest(h, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("%s status = %d, want 400", tc.name, status)
			}
		})
	}
}

// TestLoginHandlerRateLimitReturns429 proves the contract's wx-login 429 is
// reachable: once the per-IP budget is consumed the endpoint rejects instead
// of touching the usecase.
func TestLoginHandlerRateLimitReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewLoginRateLimiter(1, time.Minute)
	h, err := NewLoginHandler(LoginHandlerDeps{Usecase: &LoginUsecase{}, Limiter: limiter})
	if err != nil {
		t.Fatalf("new login handler: %v", err)
	}

	// First attempt consumes the only token. The zero-value usecase would fail
	// on Execute, but the assertion is that the SECOND attempt never reaches it.
	firstStatus, _ := performLoginRequest(h, `{"code":"first"}`)
	if firstStatus == http.StatusTooManyRequests {
		t.Fatal("first request was rate limited")
	}
	secondStatus, _ := performLoginRequest(h, `{"code":"second"}`)
	if secondStatus != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", secondStatus)
	}

	// A successful login resets the bucket, so a subsequent attempt is allowed.
	limiter.Reset(testClientIP)
	thirdStatus, _ := performLoginRequest(h, `{"code":"third"}`)
	if thirdStatus == http.StatusTooManyRequests {
		t.Fatal("third request was rate limited after reset")
	}
}

// testClientIP is the IP the performLoginRequest helper assigns to requests.
const testClientIP = "127.0.0.1"

func performLoginRequest(h *LoginHandler, body string) (int, string) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/auth/wx-login", h.Login)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/wx-login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = testClientIP + ":5555"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}
