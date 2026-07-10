package server

import (
	"strings"
	"sync"
	"time"
)

const defaultRateLimitMaxEntries = 10_000

type requestLimiter struct {
	limit      int
	window     time.Duration
	maxEntries int
	mu         sync.Mutex
	entries    map[string][]time.Time
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
		limit:      config.Limit,
		window:     config.Window,
		maxEntries: defaultRateLimitMaxEntries,
		entries:    make(map[string][]time.Time),
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
	if _, exists := limiter.entries[key]; !exists && len(limiter.entries) >= limiter.maxEntries {
		limiter.pruneExpired(cutoff)
		if len(limiter.entries) >= limiter.maxEntries {
			return false
		}
	}
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

func (limiter *requestLimiter) pruneExpired(cutoff time.Time) {
	for key, attempts := range limiter.entries {
		index := 0
		for index < len(attempts) && attempts[index].Before(cutoff) {
			index++
		}
		if index == len(attempts) {
			delete(limiter.entries, key)
			continue
		}
		if index > 0 {
			limiter.entries[key] = attempts[index:]
		}
	}
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
