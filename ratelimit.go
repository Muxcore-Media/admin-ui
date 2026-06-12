package main

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateLimitRecord struct {
	count        int
	blockedUntil time.Time
	lastActivity time.Time
}

type rateLimiter struct {
	mu        sync.Mutex
	records   map[string]*rateLimitRecord
	threshold int
	window    time.Duration
}

func newRateLimiter(threshold int, window time.Duration) *rateLimiter {
	if threshold <= 0 {
		threshold = 6
	}
	if window <= 0 {
		window = 1 * time.Minute
	}
	rl := &rateLimiter{
		records:   make(map[string]*rateLimitRecord),
		threshold: threshold,
		window:    window,
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
	if rec.count >= rl.threshold {
		rec.blockedUntil = now.Add(rl.window)
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

func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can be a comma-separated list: "client, proxy1, proxy2"
		// The leftmost address is the original client.
		if i := strings.IndexByte(xff, ','); i != -1 {
			xff = xff[:i]
		}
		xff = strings.TrimSpace(xff)
		if ip := net.ParseIP(xff); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
