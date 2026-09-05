package admin

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// LoginRateLimiter enforces TWO independent login rate-limit dimensions: a
// per-account budget and a per-IP budget. A request is allowed only when BOTH
// dimensions still have capacity. The per-IP budget stops a single attacker
// brute-forcing many accounts from one host; the per-account budget stops a
// single account being sprayed from many hosts. The two dimensions are keyed
// separately, so exhausting the per-IP budget for one host does not lock out
// the same account from other hosts until the per-account budget is spent.
type LoginRateLimiter struct {
	mu             sync.Mutex
	accountLimit   int
	accountWindow  time.Duration
	ipLimit        int
	ipWindow       time.Duration
	accountBuckets map[string]*loginRateBucket
	ipBuckets      map[string]*loginRateBucket
	now            func() time.Time
}

type loginRateBucket struct {
	started time.Time
	count   int
}

// LoginRateLimiterConfig is the two-dimensional configuration of
// NewLoginRateLimiterConfig. Each dimension carries its own limit and window.
type LoginRateLimiterConfig struct {
	AccountLimit  int
	AccountWindow time.Duration
	IPLimit       int
	IPWindow      time.Duration
}

// NewLoginRateLimiterConfig returns a LoginRateLimiter with independent
// per-account and per-IP budgets. Invalid values are clamped so the limiter
// always enforces at least one attempt per window.
func NewLoginRateLimiterConfig(cfg LoginRateLimiterConfig) *LoginRateLimiter {
	if cfg.AccountLimit < 1 {
		cfg.AccountLimit = 1
	}
	if cfg.AccountWindow <= 0 {
		cfg.AccountWindow = time.Minute
	}
	if cfg.IPLimit < 1 {
		cfg.IPLimit = 1
	}
	if cfg.IPWindow <= 0 {
		cfg.IPWindow = time.Minute
	}
	return &LoginRateLimiter{
		accountLimit:   cfg.AccountLimit,
		accountWindow:  cfg.AccountWindow,
		ipLimit:        cfg.IPLimit,
		ipWindow:       cfg.IPWindow,
		accountBuckets: make(map[string]*loginRateBucket),
		ipBuckets:      make(map[string]*loginRateBucket),
		now:            time.Now,
	}
}

// NewLoginRateLimiter returns a LoginRateLimiter where both the per-account
// and the per-IP dimension share the supplied limit and window. It is kept for
// callers that do not need to differentiate the two budgets; production wiring
// uses NewLoginRateLimiterConfig.
func NewLoginRateLimiter(limit int, window time.Duration) *LoginRateLimiter {
	return NewLoginRateLimiterConfig(LoginRateLimiterConfig{
		AccountLimit:  limit,
		AccountWindow: window,
		IPLimit:       limit,
		IPWindow:      window,
	})
}

// SetNow overrides the clock used by the limiter. It exists so tests can
// substitute a deterministic clock.
func (l *LoginRateLimiter) SetNow(now func() time.Time) {
	if l == nil {
		return
	}
	if now == nil {
		now = time.Now
	}
	l.now = now
}

// Allow reports whether the supplied (username, ip) tuple may proceed. Both
// the per-account bucket and the per-IP bucket must have capacity; the first
// dimension that is exhausted rejects the attempt. Keys are normalised so
// whitespace and case differences do not bypass the limit.
func (l *LoginRateLimiter) Allow(username, ip string) bool {
	if l == nil {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	accountKey := strings.ToLower(strings.TrimSpace(username))
	ipKey := strings.TrimSpace(ip)
	if !allowIntoBucket(l.accountBuckets, accountKey, l.accountLimit, l.accountWindow, now) {
		return false
	}
	if ipKey != "" {
		if !allowIntoBucket(l.ipBuckets, ipKey, l.ipLimit, l.ipWindow, now) {
			return false
		}
	}
	return true
}

// allowIntoBucket consumes one token from the named bucket, resetting the
// window when it has elapsed. The caller holds the limiter mutex.
func allowIntoBucket(buckets map[string]*loginRateBucket, key string, limit int, window time.Duration, now time.Time) bool {
	bucket, ok := buckets[key]
	if !ok || now.Sub(bucket.started) >= window {
		buckets[key] = &loginRateBucket{started: now, count: 1}
		return true
	}
	if bucket.count >= limit {
		return false
	}
	bucket.count++
	return true
}

// Reset clears the per-account and per-IP buckets for the supplied tuple.
// Used after a successful login so the legitimate user is not penalised by
// earlier failed attempts.
func (l *LoginRateLimiter) Reset(username, ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.accountBuckets, strings.ToLower(strings.TrimSpace(username)))
	if ip := strings.TrimSpace(ip); ip != "" {
		delete(l.ipBuckets, ip)
	}
}

// newCSRFToken returns a cryptographically random CSRF token. The handler
// issues a fresh token after a successful login and binds it to a Secure
// HttpOnly cookie with SameSite=Strict.
func newCSRFToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return ""
}
