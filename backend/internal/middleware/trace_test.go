package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestTraceMiddlewarePropagatesValidTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Trace())
	r.GET("/trace", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"trace_id": TraceID(c.Request.Context())})
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set("X-Trace-Id", "trace-test-123")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Header().Get("X-Trace-Id"); got != "trace-test-123" {
		t.Fatalf("X-Trace-Id = %q, want %q", got, "trace-test-123")
	}
	if body := res.Body.String(); body != `{"trace_id":"trace-test-123"}` {
		t.Fatalf("body = %q, want trace ID in body", body)
	}
}

func TestTraceMiddlewareReplacesInvalidTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Trace())
	r.GET("/trace", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"trace_id": TraceID(c.Request.Context())})
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set("X-Trace-Id", "invalid trace id")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	got := res.Header().Get("X-Trace-Id")
	if got == "" || got == "invalid trace id" || !ValidTraceID(got) {
		t.Fatalf("generated trace ID = %q, want a valid replacement", got)
	}
}
