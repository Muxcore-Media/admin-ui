package adminui

import (
	"log/slog"
	"sync"
	"time"
)

type rateLimitRecord struct {
	count        int
	blockedUntil time.Time
	lastActivity time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	records map[string]*rateLimitRecord
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{
		records: make(map[string]*rateLimitRecord),
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *rateLimiter) Allow(ip, path, method, userAgent string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rec, exists := rl.records[ip]
	now := time.Now()

	if !exists {
		rl.records[ip] = &rateLimitRecord{
			count:        1,
			lastActivity: now,
		}
		return true
	}

	rec.lastActivity = now

	if now.Before(rec.blockedUntil) {
		return false
	}

	rec.count++
	if rec.count >= 6 {
		rec.blockedUntil = now.Add(1 * time.Minute)
		rec.count = 0
		slog.Warn("rate limit triggered", "ip", ip, "path", path, "method", method, "user_agent", userAgent)
		return false
	}

	return true
}

func (rl *rateLimiter) Reset(ip string) {
	rl.mu.Lock()
	delete(rl.records, ip)
	rl.mu.Unlock()
}

func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-30 * time.Minute)
		for ip, rec := range rl.records {
			if rec.lastActivity.Before(cutoff) {
				delete(rl.records, ip)
			}
		}
		rl.mu.Unlock()
	}
}
