package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shop-mall/backend/internal/config"

	"github.com/gin-gonic/gin"
)

func TestMetricsEndpointRestrictedToPrivateNetwork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter(config.Config{
		AllowedOrigins: []string{"https://admin.example.test"},
		AdminCookie:    config.DefaultAdminCookieConfig(),
	}, nil, Dependencies{})

	// httptest.NewRequest defaults RemoteAddr to 192.0.2.1:1234, a public
	// TEST-NET-1 address. The private-network gate must reject it.
	public := performRequest(r, http.MethodGet, "/metrics", nil, "192.0.2.1:1234")
	if public.Code != http.StatusForbidden {
		t.Fatalf("metrics from public peer status = %d, want %d", public.Code, http.StatusForbidden)
	}

	// A spoofed X-Forwarded-For must not bypass the gate: the decision is
	// based on the TCP peer address, which a remote client cannot forge.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 127.0.0.1")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("metrics with spoofed X-Forwarded-For status = %d, want %d", res.Code, http.StatusForbidden)
	}

	// Warm the request counters so the private scrape has a series to expose.
	warm := performRequest(r, http.MethodGet, "/health/live", nil, "127.0.0.1:9999")
	if warm.Code != http.StatusOK {
		t.Fatalf("warm-up health status = %d, want %d", warm.Code, http.StatusOK)
	}

	for _, addr := range []string{"127.0.0.1:9999", "10.1.2.3:9999", "172.16.42.1:9999", "192.168.10.10:9999", "[::1]:9999"} {
		internal := performRequest(r, http.MethodGet, "/metrics", nil, addr)
		if internal.Code != http.StatusOK {
			t.Fatalf("metrics from private peer %q status = %d, want %d", addr, internal.Code, http.StatusOK)
		}
		if !strings.Contains(internal.Body.String(), "shop_mall_http_requests_total") {
			t.Fatalf("metrics from private peer %q did not expose Prometheus output", addr)
		}
	}
}

// TestMetricsCountsPanickingHandlerAs5xx is the regression test for the
// middleware-ordering finding: the metrics middleware must be registered OUTSIDE
// Recover so a panicking handler still reaches the post-c.Next() increment code
// after Recover converts the panic into a 500. With the old ordering (Recover
// before Metrics) the panic unwinds the metrics frames, the request is never
// counted, and HTTP5xxRatioHigh misses panic-derived 500s. This test fails on
// that ordering because the scrape then contains no 5xx series at all.
func TestMetricsCountsPanickingHandlerAs5xx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter(config.Config{
		AllowedOrigins: []string{"https://admin.example.test"},
		AdminCookie:    config.DefaultAdminCookieConfig(),
	}, nil, Dependencies{
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			group.GET("/panic", func(c *gin.Context) {
				panic("boom")
			})
		},
	})

	res := performRequest(r, http.MethodGet, "/api/v1/panic", nil, "127.0.0.1:1234")
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want %d", res.Code, http.StatusInternalServerError)
	}

	scrape := performRequest(r, http.MethodGet, "/metrics", nil, "127.0.0.1:1234")
	if scrape.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", scrape.Code, http.StatusOK)
	}
	if !strings.Contains(scrape.Body.String(), `shop_mall_http_requests_total{code="5xx"} 1`) {
		t.Fatalf("panicking request was not counted as 5xx: %s", scrape.Body.String())
	}
}

// TestMetricsExpositionDoesNotLeakSensitiveValues guards the T4 redaction
// contract against the metrics work: request instrumentation adds no trace
// IDs, session keys, passwords, phone numbers or addresses as label values,
// so scraping the endpoint must never echo request-visible secrets. The
// bounded label set (status class, failure reason) means the exposition cannot
// contain request data at all.
func TestMetricsExpositionDoesNotLeakSensitiveValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter(config.Config{
		AllowedOrigins: []string{"https://admin.example.test"},
		AdminCookie:    config.DefaultAdminCookieConfig(),
	}, nil, Dependencies{
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			group.POST("/echo", func(c *gin.Context) {
				c.String(http.StatusOK, "ok")
			})
			group.GET("/redirect", func(c *gin.Context) {
				c.Redirect(http.StatusFound, "/api/v1/test")
			})
			group.GET("/boom", func(c *gin.Context) {
				c.String(http.StatusInternalServerError, "boom")
			})
		},
	})

	const (
		authorization = "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.secret-session-token"
		phone         = "13800138000"
		address       = "Room 501, 88 Example Road"
		password      = "SuperSecretP@ssw0rd"
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", strings.NewReader(`{"password":"`+password+`","phone":"`+phone+`","address":"`+address+`"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("echo status = %d, want %d", res.Code, http.StatusOK)
	}
	traceID := res.Header().Get("X-Trace-Id")
	if traceID == "" {
		t.Fatal("echo response did not include X-Trace-Id")
	}

	// Produce one request per status class so every bounded label appears in
	// the exposition. 2xx already came from the echo request above.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/error"},    // 400 -> 4xx
		{http.MethodGet, "/api/v1/redirect"}, // 302 -> 3xx
		{http.MethodGet, "/api/v1/boom"},     // 500 -> 5xx
	} {
		classReq := performRequest(r, tc.method, tc.path, nil, "127.0.0.1:1234")
		if classReq.Code < 300 || classReq.Code >= 600 {
			t.Fatalf("%s returned status %d, want a 3xx/4xx/5xx", tc.path, classReq.Code)
		}
	}

	scrape := performRequest(r, http.MethodGet, "/metrics", nil, "127.0.0.1:1234")
	if scrape.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", scrape.Code, http.StatusOK)
	}
	body := scrape.Body.String()

	for _, secret := range []string{traceID, authorization, phone, address, password, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"} {
		if strings.Contains(body, secret) {
			t.Fatalf("metrics exposition leaked %q", secret)
		}
	}

	// The label set must stay bounded: only status classes may appear, and no
	// path, route, or per-status explosion is allowed.
	for _, allowed := range []string{`code="2xx"`, `code="3xx"`, `code="4xx"`, `code="5xx"`} {
		if !strings.Contains(body, allowed) {
			t.Fatalf("metrics exposition missing bounded status class label %s", allowed)
		}
	}
	for _, full := range []string{`code="200"`, `code="302"`, `code="400"`, `code="500"`} {
		if strings.Contains(body, full) {
			t.Fatalf("metrics exposition leaked a full status code %q instead of the status class", full)
		}
	}
	if strings.Contains(body, `path=`) || strings.Contains(body, `trace_id=`) || strings.Contains(body, `route=`) {
		t.Fatalf("metrics exposition leaked an unbounded label")
	}
}
