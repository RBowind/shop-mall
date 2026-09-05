package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]rateLimitEntry
	now     func() time.Time
}

type rateLimitEntry struct {
	started time.Time
	count   int
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{limit: limit, window: window, entries: make(map[string]rateLimitEntry), now: time.Now}
}

func (l *RateLimiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
		l.entries[key] = rateLimitEntry{started: now, count: 1}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}

func RateLimit(limiter *RateLimiter, key func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limiter == nil || key == nil || limiter.Allow(key(c)) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"code":     3001,
			"data":     nil,
			"message":  "rate limit exceeded",
			"trace_id": TraceIDFromGin(c),
		})
	}
}
