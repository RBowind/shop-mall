package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

type CSRFConfig struct {
	CookieName string
	LoginPath  string
}

func AdminOrigin(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, raw := range allowedOrigins {
		origin, ok := canonicalHTTPSOrigin(raw)
		if ok {
			allowed[origin] = struct{}{}
		}
	}
	return func(c *gin.Context) {
		if !isWriteMethod(c.Request.Method) {
			c.Next()
			return
		}
		origin, ok := canonicalHTTPSOrigin(c.GetHeader("Origin"))
		if !ok {
			abortForbidden(c, "invalid origin")
			return
		}
		if _, ok := allowed[origin]; !ok {
			abortForbidden(c, "invalid origin")
			return
		}
		c.Next()
	}
}

func CSRF(cfg CSRFConfig) gin.HandlerFunc {
	if cfg.CookieName == "" {
		cfg.CookieName = "csrf_token"
	}
	if cfg.LoginPath == "" {
		cfg.LoginPath = "/api/admin/v1/auth/login"
	}
	return func(c *gin.Context) {
		if !isWriteMethod(c.Request.Method) || c.FullPath() == cfg.LoginPath {
			c.Next()
			return
		}
		cookie, err := c.Cookie(cfg.CookieName)
		header := c.GetHeader("X-CSRF-Token")
		if err != nil || cookie == "" || header == "" || cookie != header {
			abortForbidden(c, "invalid csrf token")
			return
		}
		c.Next()
	}
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func canonicalHTTPSOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), true
}

func abortForbidden(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"code":     1003,
		"data":     nil,
		"message":  message,
		"trace_id": TraceIDFromGin(c),
	})
}
