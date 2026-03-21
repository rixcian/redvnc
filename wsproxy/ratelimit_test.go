package wsproxy

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_AllowedUnderLimit(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Enabled:         true,
		Window:          5 * time.Minute,
		MaxAttempts:     3,
		LockoutDuration: 1 * time.Second,
	})
	defer rl.Stop()

	ip := "192.168.1.1"

	// Record 2 failures (under limit of 3)
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)

	if !rl.IsAllowed(ip) {
		t.Fatal("should be allowed with fewer failures than max")
	}
}

func TestRateLimiter_LockedOutAfterMax(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Enabled:         true,
		Window:          5 * time.Minute,
		MaxAttempts:     3,
		LockoutDuration: 1 * time.Second,
	})
	defer rl.Stop()

	ip := "192.168.1.2"

	for i := 0; i < 3; i++ {
		rl.RecordFailure(ip)
	}

	if rl.IsAllowed(ip) {
		t.Fatal("should be locked out after max attempts")
	}
}

func TestRateLimiter_LockoutExpires(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Enabled:         true,
		Window:          5 * time.Minute,
		MaxAttempts:     3,
		LockoutDuration: 50 * time.Millisecond,
	})
	defer rl.Stop()

	ip := "192.168.1.3"

	for i := 0; i < 3; i++ {
		rl.RecordFailure(ip)
	}

	if rl.IsAllowed(ip) {
		t.Fatal("should be locked out immediately after max attempts")
	}

	time.Sleep(100 * time.Millisecond)

	if !rl.IsAllowed(ip) {
		t.Fatal("should be allowed after lockout expires")
	}
}

func TestRateLimiter_ClearOnSuccess(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Enabled:         true,
		Window:          5 * time.Minute,
		MaxAttempts:     3,
		LockoutDuration: 1 * time.Hour,
	})
	defer rl.Stop()

	ip := "192.168.1.4"

	for i := 0; i < 3; i++ {
		rl.RecordFailure(ip)
	}

	if rl.IsAllowed(ip) {
		t.Fatal("should be locked out")
	}

	rl.ClearFailures(ip)

	if !rl.IsAllowed(ip) {
		t.Fatal("should be allowed after clearing failures")
	}
}

func TestRateLimiter_EscalatingLockout(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Enabled:         true,
		Window:          5 * time.Minute,
		MaxAttempts:     5,
		LockoutDuration: 30 * time.Second,
	})
	defer rl.Stop()

	// 10 failures → 5 minute lockout
	if got := rl.lockoutDuration(10); got != 5*time.Minute {
		t.Fatalf("expected 5m lockout for 10 failures, got %v", got)
	}

	// 20 failures → 1 hour lockout
	if got := rl.lockoutDuration(20); got != 1*time.Hour {
		t.Fatalf("expected 1h lockout for 20 failures, got %v", got)
	}

	// 5 failures → base lockout (30s)
	if got := rl.lockoutDuration(5); got != 30*time.Second {
		t.Fatalf("expected 30s lockout for 5 failures, got %v", got)
	}
}

func TestRateLimiter_Disabled(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Enabled:     false,
		MaxAttempts: 1,
	})
	defer rl.Stop()

	ip := "192.168.1.6"

	for i := 0; i < 100; i++ {
		rl.RecordFailure(ip)
	}

	if !rl.IsAllowed(ip) {
		t.Fatal("should always be allowed when disabled")
	}
}

func TestRateLimiter_Concurrent(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Enabled:         true,
		Window:          5 * time.Minute,
		MaxAttempts:     100,
		LockoutDuration: 1 * time.Second,
	})
	defer rl.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.RecordFailure("10.0.0.1")
			rl.IsAllowed("10.0.0.1")
			rl.RecordFailure("10.0.0.2")
			rl.IsAllowed("10.0.0.2")
		}()
	}
	wg.Wait()
}
