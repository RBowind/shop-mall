package metrics

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shop-mall/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

// staticStats is a DBStatsReader that returns a fixed snapshot, so the pool
// collector is testable without a real database.
type staticStats struct {
	stats sql.DBStats
}

func (s staticStats) Stats() sql.DBStats { return s.stats }

func TestStatusClassMapping(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{200, "2xx"}, {299, "2xx"}, {0, "2xx"},
		{302, "3xx"}, {399, "3xx"},
		{400, "4xx"}, {429, "4xx"}, {499, "4xx"},
		{500, "5xx"}, {503, "5xx"}, {599, "5xx"},
	}
	for _, tc := range cases {
		if got := StatusClass(tc.status); got != tc.want {
			t.Fatalf("StatusClass(%d) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestRequestMiddlewareEmitsBoundedStatusClasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/bad", func(c *gin.Context) { c.String(http.StatusBadRequest, "bad") })
	r.GET("/boom", func(c *gin.Context) { c.String(http.StatusInternalServerError, "boom") })
	r.GET("/redirect", func(c *gin.Context) { c.Redirect(http.StatusFound, "/ok") })

	for _, path := range []string{"/ok", "/bad", "/boom", "/ok", "/redirect"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:1"
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
	}

	out, err := m.Scrape()
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	text := string(out)
	for _, class := range []string{`code="2xx"`, `code="3xx"`, `code="4xx"`, `code="5xx"`} {
		if !strings.Contains(text, class) {
			t.Fatalf("exposition missing %s: %s", class, text)
		}
	}
	if !strings.Contains(text, `shop_mall_http_request_duration_seconds_bucket`) {
		t.Fatalf("exposition missing latency histogram")
	}
	if strings.Contains(text, `path=`) || strings.Contains(text, `trace_id=`) {
		t.Fatalf("exposition leaked an unbounded label: %s", text)
	}
	if !strings.Contains(text, `code="2xx"} 2`) {
		t.Fatalf("expected exactly two 2xx requests, got %s", text)
	}
}

// TestMetricsMiddlewareMustPrecedeRecover pins the middleware-ordering
// invariant behind the router fix: the metrics middleware must be registered
// BEFORE Recover so a panicking handler is observed as a 5xx. Registering it
// after Recover lets the panic unwind the metrics frames, and the request is
// never counted — the exact bug that made panic-derived 500s invisible to
// HTTP5xxRatioHigh.
func TestMetricsMiddlewareMustPrecedeRecover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	makeEngine := func(metricsFirst bool) (*Metrics, *gin.Engine) {
		m := New()
		r := gin.New()
		if metricsFirst {
			r.Use(m.Middleware(), middleware.Recover(nil))
		} else {
			r.Use(middleware.Recover(nil), m.Middleware())
		}
		r.GET("/panic", func(c *gin.Context) { panic("boom") })
		return m, r
	}

	run := func(m *Metrics, r *gin.Engine) string {
		req := httptest.NewRequest(http.MethodGet, "/panic", nil)
		req.RemoteAddr = "127.0.0.1:1"
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("panic status = %d, want 500", rr.Code)
		}
		out, err := m.Scrape()
		if err != nil {
			t.Fatalf("scrape: %v", err)
		}
		return string(out)
	}

	// Correct ordering: the panic is recovered inside the metrics middleware's
	// c.Next(), so the post-increment sees the 500.
	correct := run(makeEngine(true))
	if !strings.Contains(correct, `shop_mall_http_requests_total{code="5xx"} 1`) {
		t.Fatalf("metrics-before-recover did not count the panic as 5xx: %s", correct)
	}

	// Buggy ordering: the panic unwinds past the metrics middleware, which is
	// inside Recover, and the request is never counted.
	buggy := run(makeEngine(false))
	if strings.Contains(buggy, `shop_mall_http_requests_total{code="5xx"}`) {
		t.Fatalf("recover-before-metrics unexpectedly counted the panic: %s", buggy)
	}
}

func TestMetricsEndpointIsNotCountedInItsOwnMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET(MetricsPath, gin.WrapH(m.Handler()))

	req := httptest.NewRequest(http.MethodGet, MetricsPath, nil)
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	out, err := m.Scrape()
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if strings.Contains(string(out), `shop_mall_http_requests_total{code="2xx"} 1`) {
		t.Fatalf("metrics scrape counted itself: %s", out)
	}
}

func TestDBPoolCollectorReportsPoolState(t *testing.T) {
	c := NewDBPoolCollector(staticStats{stats: sql.DBStats{
		MaxOpenConnections: 20,
		OpenConnections:    12,
		InUse:              8,
		Idle:               4,
		WaitCount:          7,
		WaitDuration:       3 * time.Second,
	}})
	m := New()
	m.RegisterDBPool(c)
	// The zero-argument scrape must work with the pool registered.
	out, err := m.Scrape()
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	text := string(out)
	for _, series := range []string{
		`shop_mall_db_conn_max_open 20`,
		`shop_mall_db_conn_open 12`,
		`shop_mall_db_conn_in_use 8`,
		`shop_mall_db_conn_idle 4`,
		`shop_mall_db_conn_wait_count_total 7`,
		`shop_mall_db_conn_wait_seconds_total 3`,
	} {
		if !strings.Contains(text, series) {
			t.Fatalf("exposition missing %q: %s", series, text)
		}
	}
}

func TestUploadLoginBackupAccessors(t *testing.T) {
	m := New()
	m.RecordUploadFailure("too_large")
	m.RecordUploadFailure("bogus_reason") // must collapse to "other"
	m.RecordLoginFailure("invalid_credentials")
	m.RecordLoginThrottled()
	now := time.Unix(1700000000, 0)
	m.SetBackupLastSuccess(now)
	m.SetImageStorageUsage(1024, 2048)

	out, err := m.Scrape()
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	text := string(out)
	for _, series := range []string{
		`shop_mall_upload_failures_total{reason="too_large"} 1`,
		`shop_mall_upload_failures_total{reason="other"} 1`,
		`shop_mall_login_failures_total{reason="invalid_credentials"} 1`,
		`shop_mall_login_throttled_total 1`,
		`shop_mall_backup_last_success_timestamp_seconds 1.7e+09`,
		`shop_mall_image_storage_used_bytes 1024`,
		`shop_mall_image_storage_capacity_bytes 2048`,
	} {
		if !strings.Contains(text, series) {
			t.Fatalf("exposition missing %q: %s", series, text)
		}
	}
}

func TestNilBundleIsSafe(t *testing.T) {
	var m *Metrics
	m.RecordUploadFailure("too_large")
	m.RecordLoginFailure("invalid_credentials")
	m.RecordLoginThrottled()
	m.SetBackupLastSuccess(time.Now())
	m.SetImageStorageUsage(1, 2)
	m.RegisterDBPool(nil)
	if out, err := m.Scrape(); err != nil || out != nil {
		t.Fatalf("nil bundle scrape = %q, %v; want nil, nil", out, err)
	}
}
