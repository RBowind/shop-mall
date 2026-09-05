package auth

import (
	"testing"
	"time"
)

// TestLoginRateLimiterEnforcesPerIPBudgetAndWindow verifies the fixed-window
// semantics independently of the HTTP handler: the budget is consumed per IP,
// windows reset, and Reset clears the bucket.
func TestLoginRateLimiterEnforcesPerIPBudgetAndWindow(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	limiter := NewLoginRateLimiter(3, time.Minute)
	limiter.SetNow(func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if !limiter.Allow("10.0.0.1") {
			t.Fatalf("attempt %d allowed=false within budget", i+1)
		}
	}
	if limiter.Allow("10.0.0.1") {
		t.Fatal("4th attempt allowed=true after budget exhausted")
	}
	// A different IP has its own independent budget.
	if !limiter.Allow("10.0.0.2") {
		t.Fatal("different IP allowed=false before its own budget is spent")
	}

	// The window elapses and the bucket resets.
	now = now.Add(2 * time.Minute)
	if !limiter.Allow("10.0.0.1") {
		t.Fatal("attempt after window reset allowed=false")
	}

	// Reset clears the bucket immediately.
	if !limiter.Allow("10.0.0.3") {
		t.Fatal("10.0.0.3 allowed=false before budget spent")
	}
	if !limiter.Allow("10.0.0.3") {
		t.Fatal("10.0.0.3 second attempt allowed=false before budget spent")
	}
	limiter.Reset("10.0.0.3")
	if !limiter.Allow("10.0.0.3") {
		t.Fatal("10.0.0.3 allowed=false after Reset")
	}
}

// TestLoginRateLimiterClampsInvalidConfiguration ensures the limiter always
// enforces at least one attempt per window.
func TestLoginRateLimiterClampsInvalidConfiguration(t *testing.T) {
	zero := NewLoginRateLimiter(0, 0)
	if zero == nil || zero.limit != 1 || zero.window != time.Minute {
		t.Fatalf("zero limit/window not clamped: %+v", zero)
	}
	if !zero.Allow("10.0.0.1") {
		t.Fatal("first attempt with clamped limiter allowed=false")
	}
	if zero.Allow("10.0.0.1") {
		t.Fatal("second attempt with clamped limiter allowed=true")
	}
}
