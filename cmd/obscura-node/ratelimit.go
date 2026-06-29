package main

// Per-IP RPC rate limiting + a global connection cap (audit #44: the RPC had no
// rate limiting and the public proxy points at it). Dependency-free token-bucket
// keyed on the non-forgeable TCP peer IP; an X-Forwarded-For client IP is honored
// ONLY when the immediate peer is loopback or an operator-allowlisted proxy
// (OBX_TRUSTED_PROXIES), so a direct attacker cannot rotate keys by spoofing XFF.
// Loopback callers with no XFF (the local operator / wallet / --ui proxy itself)
// are exempt. The connection cap is applied separately via netutil.LimitListener.

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tokenBucket struct {
	tokens float64
	last   time.Time
}

type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	rate      float64         // tokens (requests) refilled per second
	burst     float64         // bucket capacity
	trusted   map[string]bool // peer IPs whose X-Forwarded-For we honor
	lastSweep time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    envFloatPositive("OBX_RPC_RATE", 50),
		burst:   envFloatPositive("OBX_RPC_BURST", 100),
		trusted: parseIPSet(os.Getenv("OBX_TRUSTED_PROXIES")),
	}
}

// clientKey returns the rate-limit key for a request and whether it is exempt.
func (rl *rateLimiter) clientKey(r *http.Request) (key string, exempt bool) {
	host := peerIP(r.RemoteAddr)
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if xff := firstXFF(r); xff != "" {
			return xff, false // public visitor forwarded by the local --ui proxy: limit per visitor
		}
		return "", true // local operator / wallet: exempt
	}
	if host != "" && rl.trusted[host] {
		if xff := firstXFF(r); xff != "" {
			return xff, false // forwarded by an allowlisted proxy: limit the real client
		}
		// Allowlisted operator proxy with no forwarded client IP (e.g. a privacy proxy
		// that strips X-Forwarded-For): exempt. Bucketing it would throttle the WHOLE
		// site under one key. It is trusted infra and does its own upstream limiting; the
		// per-IP limit still protects against DIRECT (non-proxied) public RPC abusers.
		return "", true
	}
	if host == "" {
		return "", true // unparseable peer: do not key (let the conn cap handle abuse)
	}
	return host, false
}

// allow consumes one token for key, returning false when the bucket is empty.
func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if now.Sub(rl.lastSweep) > time.Minute { // bound memory: drop long-idle buckets
		for k, b := range rl.buckets {
			if now.Sub(b.last) > 5*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.lastSweep = now
	}

	b := rl.buckets[key]
	if b == nil {
		b = &tokenBucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// middleware enforces the per-IP limit, returning 429 with Retry-After when over.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key, exempt := rl.clientKey(r); !exempt && !rl.allow(key) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func peerIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return strings.TrimSpace(host)
}

func firstXFF(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(xff, ",")[0])
}

func parseIPSet(s string) map[string]bool {
	m := make(map[string]bool)
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	return m
}

func envFloatPositive(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}

func envIntPositive(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
