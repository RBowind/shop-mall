package metrics

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	// Namespace prefixes every metric this package exports so all series live
	// under shop_mall_*.
	Namespace = "shop_mall"

	// MetricsPath is the exact HTTP path at which the exposition is served.
	MetricsPath = "/metrics"

	// statusCodeLabel is the label carrying the bounded HTTP status class.
	statusCodeLabel = "code"
	// reasonLabel is the label carrying a bounded failure reason.
	reasonLabel = "reason"
)

// Metrics bundles every Prometheus collector this process exports plus the
// request middleware, the recless accessors for the business components and
// the exposition handler. The registry is private, so every New() call (and
// therefore every test) owns an isolated set of series; nothing is registered
// into the process-global default registry, which keeps tests independent and
// prevents duplicate-collector panics when several engines exist in one
// process.
type Metrics struct {
	registry *prometheus.Registry

	requestsTotal  *prometheus.CounterVec
	requestLatency prometheus.Histogram

	UploadFailures    *prometheus.CounterVec
	LoginFailures     *prometheus.CounterVec
	LoginThrottled    prometheus.Counter
	BackupLastSuccess prometheus.Gauge
	ImageStorageUsed  prometheus.Gauge
	ImageStorageCap   prometheus.Gauge
}

// New returns an empty Metrics bundle. Business components record through the
// exported accessors; the composition root calls RegisterDBPool once with the
// application connection pool and feeds the storage gauges as the volume
// changes.
func New() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
	m.requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "http_requests_total",
		Help:      "Total number of HTTP requests served, labelled by the bounded response status class (2xx/3xx/4xx/5xx).",
	}, []string{statusCodeLabel})
	m.requestLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: Namespace,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request latency in seconds.",
	})
	m.UploadFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "upload_failures_total",
		Help:      "Total number of failed image uploads, labelled by a bounded failure reason.",
	}, []string{reasonLabel})
	m.LoginFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "login_failures_total",
		Help:      "Total number of failed administrator logins, labelled by a bounded failure reason.",
	}, []string{reasonLabel})
	m.LoginThrottled = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "login_throttled_total",
		Help:      "Total number of login attempts rejected by the login rate limiter.",
	})
	m.BackupLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "backup_last_success_timestamp_seconds",
		Help:      "Unix timestamp of the last successful database backup. The backup job (task 16) must record SetBackupLastSuccess after every successful backup; until then the gauge stays at zero and the BackupBackupAge alert fires.",
	})
	m.ImageStorageUsed = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "image_storage_used_bytes",
		Help:      "Bytes currently used by the image volume.",
	})
	m.ImageStorageCap = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "image_storage_capacity_bytes",
		Help:      "Configured capacity of the image volume.",
	})

	m.registry.MustRegister(
		m.requestsTotal,
		m.requestLatency,
		m.UploadFailures,
		m.LoginFailures,
		m.LoginThrottled,
		m.BackupLastSuccess,
		m.ImageStorageUsed,
		m.ImageStorageCap,
	)
	return m
}

// RegisterDBPool registers a database connection-pool collector. The
// composition root calls it exactly once with the application pool; the pool
// is sampled lazily on every scrape so the gauges reflect live health.
func (m *Metrics) RegisterDBPool(c *DBPoolCollector) {
	if m == nil || m.registry == nil || c == nil {
		return
	}
	m.registry.MustRegister(c)
}

// Middleware returns the request instrumentation middleware. Requests to the
// exposition itself are excluded so a scrape does not appear in the request
// counters it feeds. Only the status class is recorded; the full status code,
// the route and the trace ID are never exported, keeping the label set bounded.
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request != nil && c.Request.URL != nil && c.Request.URL.Path == MetricsPath {
			c.Next()
			return
		}
		started := time.Now()
		c.Next()
		m.requestsTotal.WithLabelValues(StatusClass(c.Writer.Status())).Inc()
		m.requestLatency.Observe(time.Since(started).Seconds())
	}
}

// Handler returns the Prometheus text exposition handler over the registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// StatusClass maps an HTTP status code to its bounded class label. Values at
// or above 500 map to "5xx", and so on. A zero status (a handler that never
// wrote) is treated as success.
func StatusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

// RecordUploadFailure increments the upload-failure counter. Unknown reasons
// collapse to "other" so the label set stays bounded.
func (m *Metrics) RecordUploadFailure(reason string) {
	if m == nil || m.UploadFailures == nil {
		return
	}
	if !validUploadReason(reason) {
		reason = "other"
	}
	m.UploadFailures.WithLabelValues(reason).Inc()
}

// RecordLoginFailure increments the login-failure counter. Unknown reasons
// collapse to "other" so the label set stays bounded.
func (m *Metrics) RecordLoginFailure(reason string) {
	if m == nil || m.LoginFailures == nil {
		return
	}
	if !validLoginReason(reason) {
		reason = "other"
	}
	m.LoginFailures.WithLabelValues(reason).Inc()
}

// RecordLoginThrottled increments the login-throttling counter. It is fed by
// the login service whenever the rate limiter rejects an attempt.
func (m *Metrics) RecordLoginThrottled() {
	if m == nil || m.LoginThrottled == nil {
		return
	}
	m.LoginThrottled.Inc()
}

// SetBackupLastSuccess records the timestamp of the last successful database
// backup. Task 16's backup job calls this with the completion time after every
// successful backup; a zero value records "now".
func (m *Metrics) SetBackupLastSuccess(t time.Time) {
	if m == nil || m.BackupLastSuccess == nil {
		return
	}
	if t.IsZero() {
		t = time.Now().UTC()
	}
	m.BackupLastSuccess.Set(float64(t.Unix()))
}

// SetImageStorageUsage records the current used/capacity bytes of the image
// volume. It is fed by the upload handler and the cleanup loop whenever the
// volume state changes.
func (m *Metrics) SetImageStorageUsage(used, capacity int64) {
	if m == nil {
		return
	}
	if m.ImageStorageUsed != nil {
		m.ImageStorageUsed.Set(float64(used))
	}
	if m.ImageStorageCap != nil {
		m.ImageStorageCap.Set(float64(capacity))
	}
}

// Scrape renders the current registry snapshot through the same exposition
// handler used by the HTTP endpoint. Tests use it to assert on the wire
// format; it returns the exact text Prometheus would see.
func (m *Metrics) Scrape() ([]byte, error) {
	if m == nil || m.registry == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	handler := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, &http.Request{})
	buf.Write(recorder.Body.Bytes())
	return buf.Bytes(), nil
}

var validUploadReasons = map[string]struct{}{
	"missing_file":         {},
	"too_large":            {},
	"read_failed":          {},
	"unsupported_format":   {},
	"too_many_pixels":      {},
	"invalid_image":        {},
	"invalid_upload":       {},
	"storage_check_failed": {},
	"capacity_exceeded":    {},
	"store_failed":         {},
	"other":                {},
}

func validUploadReason(reason string) bool {
	_, ok := validUploadReasons[reason]
	return ok
}

var validLoginReasons = map[string]struct{}{
	"invalid_credentials": {},
	"account_disabled":    {},
	"other":               {},
}

func validLoginReason(reason string) bool {
	_, ok := validLoginReasons[reason]
	return ok
}
