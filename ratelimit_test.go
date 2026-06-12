package main

import (
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	rl := newRateLimiter(6, time.Minute)
	if !rl.Allow("192.168.1.1", "/login", "POST", "test-agent") {
		t.Fatal("expected first request to be allowed")
	}
}

func TestRateLimiterBlocksAfterThreshold(t *testing.T) {
	rl := newRateLimiter(6, time.Minute)
	ip := "10.0.0.1"

	// 5 allowed requests
	for i := 0; i < 5; i++ {
		if !rl.Allow(ip, "/login", "POST", "test") {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	// 6th triggers block
	if rl.Allow(ip, "/login", "POST", "test") {
		t.Fatal("expected 6th request to be blocked")
	}

	// Immediately after, still blocked
	if rl.Allow(ip, "/login", "POST", "test") {
		t.Fatal("expected request to be blocked (still in cooldown)")
	}
}

func TestRateLimiterReset(t *testing.T) {
	rl := newRateLimiter(6, time.Minute)
	ip := "10.0.0.2"

	for i := 0; i < 6; i++ {
		rl.Allow(ip, "/login", "POST", "test")
	}

	if rl.Allow(ip, "/login", "POST", "test") {
		t.Fatal("expected request to be blocked before reset")
	}

	rl.Reset(ip)

	if !rl.Allow(ip, "/login", "POST", "test") {
		t.Fatal("expected request to be allowed after reset")
	}
}

func TestRateLimiterSeparateIPs(t *testing.T) {
	rl := newRateLimiter(6, time.Minute)

	// Exhaust one IP
	for i := 0; i < 6; i++ {
		rl.Allow("10.0.0.1", "/login", "POST", "test")
	}

	// Different IP should be unaffected
	if !rl.Allow("10.0.0.2", "/login", "POST", "test") {
		t.Fatal("expected different IP to be allowed")
	}
}

func TestExtractIP(t *testing.T) {
	r := mustRequest("GET", "/")
	if ip := extractIP(r); ip == "" {
		t.Fatal("expected non-empty IP")
	}
}

func TestExtractIPWithXFF(t *testing.T) {
	r := mustRequest("GET", "/")
	r.Header.Set("X-Forwarded-For", "203.0.113.1")
	if ip := extractIP(r); ip != "203.0.113.1" {
		t.Fatalf("expected 203.0.113.1, got %s", ip)
	}
}
