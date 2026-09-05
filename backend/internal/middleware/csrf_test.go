package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAdminWriteRejectsMissingOrInvalidOrigin(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		origin string
	}{
		{name: "missing", origin: ""},
		{name: "wrong origin", origin: "https://evil.example.test"},
		{name: "insecure origin", origin: "http://admin.example.test"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			r := gin.New()
			r.Use(AdminOrigin([]string{"https://admin.example.test"}))
			r.POST("/api/admin/v1/products", func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/products", nil)
			if testCase.origin != "" {
				req.Header.Set("Origin", testCase.origin)
			}
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)

			if res.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusForbidden)
			}
		})
	}
}

func TestAdminWriteRequiresMatchingCSRFHeaderAndCookie(t *testing.T) {
	r := gin.New()
	r.Use(CSRF(CSRFConfig{CookieName: "csrf_token", LoginPath: "/api/admin/v1/auth/login"}))
	r.POST("/api/admin/v1/products", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, testCase := range []struct {
		name   string
		header string
		cookie string
		want   int
	}{
		{name: "missing both", want: http.StatusForbidden},
		{name: "header only", header: "csrf-1", want: http.StatusForbidden},
		{name: "cookie only", cookie: "csrf-1", want: http.StatusForbidden},
		{name: "mismatch", header: "csrf-2", cookie: "csrf-1", want: http.StatusForbidden},
		{name: "matching", header: "csrf-1", cookie: "csrf-1", want: http.StatusNoContent},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/products", nil)
			if testCase.header != "" {
				req.Header.Set("X-CSRF-Token", testCase.header)
			}
			if testCase.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "csrf_token", Value: testCase.cookie})
			}
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)

			if res.Code != testCase.want {
				t.Fatalf("status = %d, want %d", res.Code, testCase.want)
			}
		})
	}
}

func TestAdminLoginSkipsCSRFButStillRequiresOrigin(t *testing.T) {
	r := gin.New()
	r.Use(AdminOrigin([]string{"https://admin.example.test"}))
	r.Use(CSRF(CSRFConfig{CookieName: "csrf_token", LoginPath: "/api/admin/v1/auth/login"}))
	r.POST("/api/admin/v1/auth/login", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/login", nil)
	req.Header.Set("Origin", "https://admin.example.test")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
}
