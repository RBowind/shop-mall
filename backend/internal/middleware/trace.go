package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const TraceHeader = "X-Trace-Id"

type traceIDContextKey struct{}

func Trace() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(TraceHeader)
		if !ValidTraceID(traceID) {
			traceID = NewTraceID()
		}
		c.Set(stringTraceIDKey, traceID)
		c.Request = c.Request.WithContext(WithTraceID(c.Request.Context(), traceID))
		c.Header(TraceHeader, traceID)
		c.Next()
	}
}

const stringTraceIDKey = "request.trace_id"

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDContextKey{}, traceID)
}

func TraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	traceID, _ := ctx.Value(traceIDContextKey{}).(string)
	return traceID
}

func TraceIDFromGin(c *gin.Context) string {
	if value, ok := c.Get(stringTraceIDKey); ok {
		if traceID, ok := value.(string); ok {
			return traceID
		}
	}
	return TraceID(c.Request.Context())
}

func ValidTraceID(traceID string) bool {
	if traceID == "" || len(traceID) > 128 || strings.TrimSpace(traceID) != traceID {
		return false
	}
	for _, ch := range traceID {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '.' && ch != '_' && ch != ':' && ch != '-' {
			return false
		}
	}
	return true
}

func NewTraceID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return "trace-unknown"
}

func EnsureTraceHeader(w http.ResponseWriter, traceID string) {
	if ValidTraceID(traceID) {
		w.Header().Set(TraceHeader, traceID)
	}
}
