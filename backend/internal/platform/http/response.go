package http

import (
	"net/http"

	"shop-mall/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

const (
	CodeOK             = 0
	CodeBadRequest     = 1001
	CodeUnauthorized   = 1002
	CodeForbidden      = 1003
	CodeNotFound       = 1004
	CodeConflict       = 2001
	CodeBusiness       = 2002
	CodeRateLimited    = 3001
	CodePayloadTooBig  = 3002
	CodeUnsupported    = 4001
	CodeStorageFailure = 4002
	CodeInternal       = 5001
)

type Envelope struct {
	Code    int    `json:"code"`
	Data    any    `json:"data"`
	Message string `json:"message"`
	TraceID string `json:"trace_id"`
}

func Success(c *gin.Context, status int, data any) {
	traceID := ensureTraceID(c)
	c.JSON(status, Envelope{Code: CodeOK, Data: data, Message: "ok", TraceID: traceID})
}

func Error(c *gin.Context, status, code int, message string) {
	traceID := ensureTraceID(c)
	c.AbortWithStatusJSON(status, Envelope{Code: code, Data: nil, Message: message, TraceID: traceID})
}

func ensureTraceID(c *gin.Context) string {
	traceID := middleware.TraceIDFromGin(c)
	if !middleware.ValidTraceID(traceID) {
		traceID = middleware.NewTraceID()
		c.Set("request.trace_id", traceID)
		c.Request = c.Request.WithContext(middleware.WithTraceID(c.Request.Context(), traceID))
	}
	c.Header(middleware.TraceHeader, traceID)
	return traceID
}

func OperationalJSON(c *gin.Context, status int, value any) {
	c.JSON(status, value)
}

func StatusForError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return http.StatusInternalServerError
}
