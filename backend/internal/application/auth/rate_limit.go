package auth

import (
	"strings"
	"sync"
	"time"
)

// LoginRateLimiter enforces a per-IP budget for wx-login. The buyer account is
// not known before the WeChat code is exchanged, so a per-account dimension is
// impossible at this layer; the per-IP budget stops a single host spraying many
// login codes. It mirrors the admin limiter's fixed-window bucket semantics.
type LoginRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*loginRateBucket
	now     func() time.Time
}

type loginRateBucket struct {
	started time.Time
	count   int
}

// NewLoginRateLimiter returns a per-IP limiter with the supplied budget.
// Invalid values are clamped so the limiter always enforces at least one
// attempt per window.
func NewLoginRateLimiter(limit int, window time.Duration) *LoginRateLimiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &LoginRateLimiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*loginRateBucket),
		now:     time.Now,
	}
}

// SetNow overrides the clock used by the limiter so tests can substitute a
// deterministic clock.
func (l *LoginRateLimiter) SetNow(now func() time.Time) {
	if l == nil {
		return
	}
	if now == nil {
		now = time.Now
	}
	l.now = now
}

// Allow reports whether the supplied client IP may proceed, consuming one
// token from its bucket. Whitespace-normalised and empty keys share the empty
// bucket so a request without a detectable IP cannot bypass the limit.
func (l *LoginRateLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	key := strings.TrimSpace(ip)
	bucket, ok := l.buckets[key]
	if !ok || now.Sub(bucket.started) >= l.window {
		l.buckets[key] = &loginRateBucket{started: now, count: 1}
		return true
	}
	if bucket.count >= l.limit {
		return false
	}
	bucket.count++
	return true
}

// Reset clears the per-IP bucket after a successful login so a legitimate
// buyer is not penalised by earlier failed attempts.
func (l *LoginRateLimiter) Reset(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, strings.TrimSpace(ip))
}
