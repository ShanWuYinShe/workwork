package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type rateLimitEntry struct {
	timestamps []time.Time
	blockedAt  time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	entries  map[string]*rateLimitEntry
	maxReqs  int           // max requests in the window
	window   time.Duration // sliding window
	banTime  time.Duration // how long to ban
	stopChan chan struct{}
}

var authRateLimiter = &rateLimiter{
	entries: make(map[string]*rateLimitEntry),
	maxReqs: 20,               // 20 requests
	window:  10 * time.Second, // per 10 seconds
	banTime: 30 * time.Minute, // ban for 30 minutes
}

func (rl *rateLimiter) start() {
	rl.stopChan = make(chan struct{})
	go rl.cleanupLoop()
}

func (rl *rateLimiter) stop() {
	close(rl.stopChan)
}

func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.cleanup()
			ipBlocker.cleanupExpired()
		case <-rl.stopChan:
			return
		}
	}
}

func (rl *rateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, e := range rl.entries {
		// Remove expired entries
		if !e.blockedAt.IsZero() && now.Sub(e.blockedAt) > rl.banTime {
			delete(rl.entries, ip)
			continue
		}
		// Prune old timestamps
		cutoff := now.Add(-rl.window)
		var kept []time.Time
		for _, t := range e.timestamps {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		e.timestamps = kept
		if len(kept) == 0 && e.blockedAt.IsZero() {
			delete(rl.entries, ip)
		}
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()

	e, exists := rl.entries[ip]
	if !exists {
		e = &rateLimitEntry{}
		rl.entries[ip] = e
	}

	// Check if currently blocked
	if !e.blockedAt.IsZero() {
		if now.Sub(e.blockedAt) < rl.banTime {
			return false
		}
		// Ban expired, reset
		e.blockedAt = time.Time{}
		e.timestamps = nil
	}

	// Prune old timestamps
	cutoff := now.Add(-rl.window)
	var kept []time.Time
	for _, t := range e.timestamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.timestamps = kept

	// Check threshold
	if len(e.timestamps) >= rl.maxReqs {
		e.blockedAt = now
		// Also add to persistent IP blocker
		ipBlocker.add(ip, "rate limit exceeded", rl.banTime)
		ipBlocker.save()
		return false
	}

	// Record this request
	e.timestamps = append(e.timestamps, now)
	return true
}

// rateLimitMiddleware should be placed before ipBlockMiddleware so repeated
// abusive requests can be persisted into the IP block list.
func rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Internal service requests (fail2ban etc.) bypass rate limiting
		if isPrivilegedServiceRequest(r) {
			next(w, r)
			return
		}
		ip := clientIP(r)
		if ip != "" && !authRateLimiter.allow(ip) {
			writeError(w, "Too many requests, try again later", 429)
			return
		}
		next(w, r)
	}
}

func apiRateLimitStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authRateLimiter.mu.Lock()
		defer authRateLimiter.mu.Unlock()
		var blocked []map[string]any
		now := time.Now()
		for ip, e := range authRateLimiter.entries {
			if !e.blockedAt.IsZero() && now.Sub(e.blockedAt) < authRateLimiter.banTime {
				blocked = append(blocked, map[string]any{
					"ip":         ip,
					"blocked_at": e.blockedAt.Format(time.RFC3339),
					"until":      e.blockedAt.Add(authRateLimiter.banTime).Format(time.RFC3339),
					"count":      len(e.timestamps),
				})
			}
		}
		writeJSON(w, map[string]any{"blocked": blocked})
	}
}

func apiRateLimitUnblock() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IP string `json:"ip"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.IP == "" {
			writeError(w, "IP required", 400)
			return
		}
		if net.ParseIP(body.IP) == nil {
			writeError(w, "Invalid IP address", 400)
			return
		}
		authRateLimiter.mu.Lock()
		if e, ok := authRateLimiter.entries[body.IP]; ok {
			e.blockedAt = time.Time{}
			e.timestamps = nil
		}
		authRateLimiter.mu.Unlock()
		ipBlocker.remove(body.IP)
		ipBlocker.save()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}
