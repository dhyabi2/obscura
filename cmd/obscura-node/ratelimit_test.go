package main

import (
	"net/http"
	"testing"
)

func newReq(remote, xff string) *http.Request {
	r, _ := http.NewRequest("GET", "/height", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestRateLimiterBurstThenThrottle(t *testing.T) {
	rl := &rateLimiter{buckets: map[string]*tokenBucket{}, rate: 1, burst: 5, trusted: map[string]bool{}}
	// A public peer gets at most `burst` immediate requests, then is throttled.
	allowed := 0
	for i := 0; i < 20; i++ {
		if rl.allow("203.0.113.7") {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("burst: allowed=%d, want 5", allowed)
	}
}

func TestRateLimiterLoopbackExempt(t *testing.T) {
	rl := &rateLimiter{buckets: map[string]*tokenBucket{}, rate: 1, burst: 1, trusted: map[string]bool{}}
	_, exempt := rl.clientKey(newReq("127.0.0.1:55000", ""))
	if !exempt {
		t.Fatal("loopback with no XFF must be exempt")
	}
	// Loopback WITH an XFF (local --ui proxy forwarding a public visitor) keys on the visitor.
	key, exempt := rl.clientKey(newReq("127.0.0.1:55000", "198.51.100.9"))
	if exempt || key != "198.51.100.9" {
		t.Fatalf("loopback+XFF: key=%q exempt=%v, want 198.51.100.9/false", key, exempt)
	}
}

func TestRateLimiterXFFOnlyHonoredFromTrustedPeer(t *testing.T) {
	rl := &rateLimiter{buckets: map[string]*tokenBucket{}, rate: 1, burst: 1, trusted: map[string]bool{"10.0.0.1": true}}
	// Untrusted public peer cannot rotate keys via spoofed XFF: keyed on its real IP.
	key, _ := rl.clientKey(newReq("203.0.113.7:40000", "1.2.3.4"))
	if key != "203.0.113.7" {
		t.Fatalf("untrusted XFF must be ignored: key=%q, want 203.0.113.7", key)
	}
	// A trusted proxy's forwarded client IP IS honored.
	key, _ = rl.clientKey(newReq("10.0.0.1:40000", "1.2.3.4"))
	if key != "1.2.3.4" {
		t.Fatalf("trusted-proxy XFF must be honored: key=%q, want 1.2.3.4", key)
	}
	// A trusted proxy that STRIPS XFF (privacy proxy) is exempt, not bucketed under one key
	// (otherwise the whole site shares one bucket and gets throttled during block-scan sync).
	_, exempt := rl.clientKey(newReq("10.0.0.1:40000", ""))
	if !exempt {
		t.Fatal("trusted proxy with no XFF must be exempt, not bucketed")
	}
}
