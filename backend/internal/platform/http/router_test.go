package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shop-mall/backend/internal/config"

	"github.com/gin-gonic/gin"
)

func TestNewRouterSeparatesOperationalAndBusinessResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Config{
		AllowedOrigins: []string{"https://admin.example.test"},
		AdminCookie:    config.DefaultAdminCookieConfig(),
	}
	r := NewRouter(cfg, nil, Dependencies{
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			group.GET("/test", func(c *gin.Context) {
				Success(c, http.StatusOK, gin.H{"value": "ok"})
			})
			group.GET("/error", func(c *gin.Context) {
				Error(c, http.StatusBadRequest, CodeBadRequest, "bad request")
			})
		},
	})

	live := performRequest(r, http.MethodGet, "/health/live", nil, "127.0.0.1:1234")
	if live.Code != http.StatusOK {
		t.Fatalf("live status = %d, want %d", live.Code, http.StatusOK)
	}
	if strings.Contains(live.Body.String(), `"code"`) {
		t.Fatalf("live response unexpectedly used business envelope: %s", live.Body.String())
	}
	if live.Header().Get("X-Trace-Id") == "" {
		t.Fatal("live response did not include X-Trace-Id")
	}

	business := performRequest(r, http.MethodGet, "/api/v1/test", nil, "127.0.0.1:1234")
	if business.Code != http.StatusOK {
		t.Fatalf("business status = %d, want %d", business.Code, http.StatusOK)
	}
	var envelope Envelope
	if err := json.Unmarshal(business.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode business envelope: %v", err)
	}
	if envelope.TraceID == "" || envelope.TraceID != business.Header().Get("X-Trace-Id") {
		t.Fatalf("business trace mismatch: body=%q header=%q", envelope.TraceID, business.Header().Get("X-Trace-Id"))
	}

	requestError := performRequest(r, http.MethodGet, "/api/v1/error", nil, "127.0.0.1:1234")
	if requestError.Code != http.StatusBadRequest {
		t.Fatalf("business error status = %d, want %d", requestError.Code, http.StatusBadRequest)
	}
	var errorEnvelope Envelope
	if err := json.Unmarshal(requestError.Body.Bytes(), &errorEnvelope); err != nil {
		t.Fatalf("decode business error envelope: %v", err)
	}
	if errorEnvelope.Code != CodeBadRequest || errorEnvelope.TraceID == "" || errorEnvelope.TraceID != requestError.Header().Get("X-Trace-Id") {
		t.Fatalf("business error envelope mismatch: %+v header=%q", errorEnvelope, requestError.Header().Get("X-Trace-Id"))
	}

	metrics := performRequest(r, http.MethodGet, "/metrics", nil, "127.0.0.1:1234")
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", metrics.Code, http.StatusOK)
	}
	if !strings.Contains(metrics.Body.String(), "shop_mall_http_requests_total") || strings.Contains(metrics.Body.String(), `"code"`) {
		t.Fatalf("metrics response was not raw Prometheus output: %s", metrics.Body.String())
	}
}

func TestNewRouterMountsDevImagesOnlyInDevelopment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Production-style config: no /static mount. Listing routes must not
	// contain an images handler.
	prodCfg := config.Config{
		AllowedOrigins:       []string{"https://admin.example.test"},
		AdminCookie:          config.DefaultAdminCookieConfig(),
		UploadLimit:          1024,
		SignupBonus:          100,
		ImageMaxPixels:       1000,
		ImageStorageCapacity: 1024,
	}
	prodRouter := NewRouter(prodCfg, nil, Dependencies{})
	for _, route := range prodRouter.Routes() {
		if strings.HasPrefix(route.Path, "/static/images/") {
			t.Fatalf("production must not expose /static/images: %s", route.Path)
		}
	}

	// Development config with a real dir -> /static mount exists.
	devDir := t.TempDir()
	devCfg := prodCfg
	devCfg.Environment = "development"
	devCfg.ImageVolumeDir = devDir
	devRouter := NewRouter(devCfg, nil, Dependencies{})
	mounted := false
	for _, route := range devRouter.Routes() {
		if strings.HasPrefix(route.Path, "/static/images/") {
			mounted = true
			break
		}
	}
	if !mounted {
		t.Fatal("development must mount /static/images onto the image volume dir")
	}
}

func TestNewRouterReadinessRequiresDatabase(t *testing.T) {
	r := NewRouter(config.Config{}, nil, Dependencies{})
	res := performRequest(r, http.MethodGet, "/health/ready", nil, "127.0.0.1:1234")
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d", res.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(res.Body.String(), `"code"`) {
		t.Fatalf("ready response unexpectedly used business envelope: %s", res.Body.String())
	}
}

func performRequest(r http.Handler, method, path string, body *strings.Reader, remoteAddr string) *httptest.ResponseRecorder {
	var requestBody *strings.Reader
	if body != nil {
		requestBody = body
	} else {
		requestBody = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, requestBody)
	req.RemoteAddr = remoteAddr
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}
