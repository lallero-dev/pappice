package server

import (
	"testing"
	"time"
)

func TestRequestLimiterBoundsDistinctKeys(t *testing.T) {
	limiter := newRequestLimiter(RateLimit{Limit: 2, Window: time.Minute})
	limiter.maxEntries = 2
	now := time.Now().UTC()

	if !limiter.Allow("first", now) || !limiter.Allow("second", now) {
		t.Fatal("limiter rejected entries below its key bound")
	}
	if limiter.Allow("third", now) {
		t.Fatal("limiter accepted a new key above its bound")
	}
	if got := len(limiter.entries); got != 2 {
		t.Fatalf("limiter entries = %d, want 2", got)
	}

	later := now.Add(time.Minute + time.Nanosecond)
	if !limiter.Allow("third", later) {
		t.Fatal("limiter did not reclaim expired keys")
	}
	if got := len(limiter.entries); got != 1 {
		t.Fatalf("limiter entries after pruning = %d, want 1", got)
	}
}
