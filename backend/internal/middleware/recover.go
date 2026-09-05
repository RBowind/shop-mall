package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

func Recover(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if logger != nil {
					logger.ErrorContext(c.Request.Context(), "panic recovered", "panic_type", "redacted")
				}
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code":     5001,
					"data":     nil,
					"message":  "internal server error",
					"trace_id": TraceIDFromGin(c),
				})
			}
		}()
		c.Next()
	}
}
