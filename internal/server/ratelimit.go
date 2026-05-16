package server

import (
	"strings"
	"sync"
	"time"
)

type requestLimiter struct {
	limit   int
	window  time.Duration
	mu      sync.Mutex
	entries map[string][]time.Time
}

func withDefaultRateLimit(config RateLimit, limit int, window time.Duration) RateLimit {
	if config.Limit <= 0 {
		config.Limit = limit
	}
	if config.Window <= 0 {
		config.Window = window
	}
	return config
}

func newRequestLimiter(config RateLimit) *requestLimiter {
	return &requestLimiter{
		limit:   config.Limit,
		window:  config.Window,
		entries: make(map[string][]time.Time),
	}
}

func (limiter *requestLimiter) Allow(key string, now time.Time) bool {
	key = strings.TrimSpace(key)
	if limiter == nil || limiter.limit <= 0 || limiter.window <= 0 || key == "" {
		return true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	cutoff := now.Add(-limiter.window)
	attempts := limiter.entries[key]
	index := 0
	for index < len(attempts) && attempts[index].Before(cutoff) {
		index++
	}
	if index > 0 {
		attempts = attempts[index:]
	}
	if len(attempts) >= limiter.limit {
		limiter.entries[key] = attempts
		return false
	}
	attempts = append(attempts, now)
	limiter.entries[key] = attempts
	return true
}

func (limiter *requestLimiter) Reset(key string) {
	key = strings.TrimSpace(key)
	if limiter == nil || key == "" {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	delete(limiter.entries, key)
}
