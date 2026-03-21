package wsproxy

import (
	"sync"
	"time"
)

// RateLimitConfig holds configuration for the auth rate limiter.
type RateLimitConfig struct {
	// Enabled controls whether rate limiting is active. Default: true.
	Enabled bool

	// Window is the sliding window for counting failures. Default: 5m.
	Window time.Duration

	// MaxAttempts is the number of failures before lockout kicks in. Default: 5.
	MaxAttempts int

	// LockoutDuration is the base lockout after MaxAttempts failures. Default: 30s.
	LockoutDuration time.Duration
}

// DefaultRateLimitConfig returns sensible defaults for rate limiting.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:         true,
		Window:          5 * time.Minute,
		MaxAttempts:     5,
		LockoutDuration: 30 * time.Second,
	}
}

// RateLimiter tracks failed authentication attempts per IP address and
// enforces escalating lockouts.
type RateLimiter struct {
	config RateLimitConfig

	mu       sync.RWMutex
	failures map[string][]time.Time // IP → timestamps of failures within window
	stopCh   chan struct{}
}

// NewRateLimiter creates a new rate limiter with the given config.
// Call Stop() when done to clean up the background goroutine.
func NewRateLimiter(config RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		config:   config,
		failures: make(map[string][]time.Time),
		stopCh:   make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

// IsAllowed checks whether the given IP is allowed to attempt auth.
// Returns true if allowed, false if locked out.
func (rl *RateLimiter) IsAllowed(ip string) bool {
	if !rl.config.Enabled {
		return true
	}

	rl.mu.RLock()
	defer rl.mu.RUnlock()

	timestamps := rl.failures[ip]
	if len(timestamps) == 0 {
		return true
	}

	now := time.Now()
	count := rl.countRecent(timestamps, now)
	if count < rl.config.MaxAttempts {
		return true
	}

	// Determine lockout duration based on failure count
	lockout := rl.lockoutDuration(count)

	// Check if last failure + lockout is still in the future
	lastFailure := timestamps[len(timestamps)-1]
	return now.After(lastFailure.Add(lockout))
}

// RecordFailure records a failed auth attempt for the given IP.
func (rl *RateLimiter) RecordFailure(ip string) {
	if !rl.config.Enabled {
		return
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.failures[ip] = append(rl.failures[ip], time.Now())
}

// ClearFailures removes all failure records for the given IP (e.g. on successful auth).
func (rl *RateLimiter) ClearFailures(ip string) {
	if !rl.config.Enabled {
		return
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	delete(rl.failures, ip)
}

// Stop stops the background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// countRecent counts failures within the configured window.
func (rl *RateLimiter) countRecent(timestamps []time.Time, now time.Time) int {
	cutoff := now.Add(-rl.config.Window)
	count := 0
	for _, t := range timestamps {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// lockoutDuration returns the lockout duration based on the number of recent failures.
// 5+ failures: 30s, 10+ failures: 5m, 20+ failures: 1h.
func (rl *RateLimiter) lockoutDuration(failCount int) time.Duration {
	switch {
	case failCount >= 20:
		return 1 * time.Hour
	case failCount >= 10:
		return 5 * time.Minute
	default:
		return rl.config.LockoutDuration
	}
}

// cleanup periodically removes expired entries from the failures map.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.purgeExpired()
		}
	}
}

// purgeExpired removes failure entries older than the window.
func (rl *RateLimiter) purgeExpired() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.config.Window)

	for ip, timestamps := range rl.failures {
		// Find first non-expired entry
		valid := 0
		for _, t := range timestamps {
			if t.After(cutoff) {
				break
			}
			valid++
		}
		if valid == len(timestamps) {
			delete(rl.failures, ip)
		} else if valid > 0 {
			rl.failures[ip] = timestamps[valid:]
		}
	}
}
