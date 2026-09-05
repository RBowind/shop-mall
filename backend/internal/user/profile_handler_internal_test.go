package user

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func TestMapProfileError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   int
	}{
		{"missing user", ErrUserNotFound, http.StatusNotFound, 1004},
		{"internal failure", errors.New("database down"), http.StatusInternalServerError, 5001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code := mapProfileError(tc.err)
			if status != tc.wantStatus || code != tc.wantCode {
				t.Fatalf("mapProfileError(%v) = (%d, %d), want (%d, %d)", tc.err, status, code, tc.wantStatus, tc.wantCode)
			}
		})
	}
}

// TestProfileHandlerValidationPathsRequiresNoDatabase exercises the request
// validation that runs before the service is reached: missing identity is a
// 401, an empty update and malformed JSON are 400, and an oversized nickname is
// a 422 business error.
func TestProfileHandlerValidationPathsRequiresNoDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// A zero-value service is fine here: every case below returns before a
	// service method is invoked.
	h, err := NewProfileHandler(ProfileHandlerDeps{Service: &Service{}})
	if err != nil {
		t.Fatalf("new profile handler: %v", err)
	}

	// No buyer subject in the middleware context → 401.
	status, _ := performProfileRequest(h, http.MethodGet, "/me", "", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("GET /me without identity status = %d, want 401", status)
	}

	// Empty update ({}) → 400.
	status, _ = performProfileRequest(h, http.MethodPatch, "/me", "buyer:"+uid.New().String(), `{}`)
	if status != http.StatusBadRequest {
		t.Fatalf("empty PATCH /me status = %d, want 400", status)
	}

	// Malformed JSON → 400.
	status, _ = performProfileRequest(h, http.MethodPatch, "/me", "buyer:"+uid.New().String(), "{not-json")
	if status != http.StatusBadRequest {
		t.Fatalf("malformed PATCH /me status = %d, want 400", status)
	}

	// Nickname over the contract's 64-character limit → 422.
	longNickname := strings.Repeat("a", 65)
	status, _ = performProfileRequest(h, http.MethodPatch, "/me", "buyer:"+uid.New().String(), `{"nickname":"`+longNickname+`"}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("oversized nickname status = %d, want 422", status)
	}
}

func performProfileRequest(h *ProfileHandler, method, path, subject, body string) (int, string) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if subject != "" {
		r.Use(func(c *gin.Context) {
			c.Set(middleware.BuyerSubjectKey, subject)
			c.Next()
		})
	}
	r.GET("/me", h.GetMe)
	r.PATCH("/me", h.UpdateMe)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}
