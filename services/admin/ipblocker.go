package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type ipBlockEntry struct {
	IP        string `json:"ip"`
	Reason    string `json:"reason"`
	BlockedAt string `json:"blocked_at"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type ipBlockStore struct {
	mu      sync.Mutex
	path    string
	Entries []ipBlockEntry `json:"entries"`
}

var ipBlocker = &ipBlockStore{path: "/app/data/blocked-ips.json"}

func (s *ipBlockStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("WARNING: failed to read blocked-ips.json: %v", err)
		}
		return
	}
	if err := json.Unmarshal(data, s); err != nil {
		log.Printf("WARNING: failed to parse blocked-ips.json: %v", err)
	}
}

func (s *ipBlockStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		log.Printf("ERROR: failed to marshal blocked IPs: %v", err)
		return
	}
	if err := atomicWriteFile(s.path, b, 0600); err != nil {
		log.Printf("ERROR: failed to write blocked IPs: %v", err)
	}
	// 同步到 nginx 层(去抖 + 异步):让 /proxy/query、媒体 API 等非 admin 面
	// 也执行同一封禁。只调度不阻塞——save 可能在限流热路径持锁调用。
	scheduleBlocklistSync()
}

func (s *ipBlockStore) isBlocked(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.Entries {
		if e.IP == ip && !blockEntryExpired(e) {
			return true
		}
	}
	return false
}

func (s *ipBlockStore) add(ip, reason string, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.Entries {
		if e.IP == ip {
			s.Entries[i].Reason = reason
			s.Entries[i].BlockedAt = time.Now().UTC().Format(time.RFC3339)
			if duration > 0 {
				s.Entries[i].ExpiresAt = time.Now().UTC().Add(duration).Format(time.RFC3339)
			} else {
				s.Entries[i].ExpiresAt = ""
			}
			return
		}
	}
	e := ipBlockEntry{
		IP:        ip,
		Reason:    reason,
		BlockedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if duration > 0 {
		e.ExpiresAt = time.Now().UTC().Add(duration).Format(time.RFC3339)
	}
	s.Entries = append(s.Entries, e)
}

func (s *ipBlockStore) remove(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []ipBlockEntry
	for _, e := range s.Entries {
		if e.IP != ip {
			kept = append(kept, e)
		}
	}
	s.Entries = kept
}

// cleanupExpired 移除已过期的封禁条目，防止 blocked-ips.json 无限增长。
func (s *ipBlockStore) cleanupExpired() {
	s.mu.Lock()
	var kept []ipBlockEntry
	for _, e := range s.Entries {
		if e.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, e.ExpiresAt); err == nil && time.Now().UTC().After(t) {
				continue
			}
		}
		kept = append(kept, e)
	}
	changed := len(kept) != len(s.Entries)
	s.Entries = kept
	s.mu.Unlock()
	if changed {
		s.save()
	}
}

func (s *ipBlockStore) list() []ipBlockEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ipBlockEntry, len(s.Entries))
	copy(out, s.Entries)
	return out
}

func clientIP(r *http.Request) string {
	if isTrustedProxy(r.RemoteAddr) {
		if ip := headerIP(r.Header.Get("X-Real-IP")); ip != nil {
			return ip.String()
		}
		if ip := forwardedForIP(r.Header.Get("X-Forwarded-For")); ip != nil {
			return ip.String()
		}
	}
	if ip := remoteIP(r.RemoteAddr); ip != nil {
		return ip.String()
	}
	return ""
}

func headerIP(raw string) net.IP {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	return net.ParseIP(raw)
}

func forwardedForIP(raw string) net.IP {
	for _, part := range strings.Split(raw, ",") {
		if ip := headerIP(part); ip != nil {
			return ip
		}
	}
	return nil
}

func ipBlockMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Internal service calls are authenticated by shared secret and must
		// still work when fail2ban has just blocked an external client IP.
		if isPrivilegedServiceRequest(r) {
			next(w, r)
			return
		}
		ip := clientIP(r)
		if ip != "" && ipBlocker.isBlocked(ip) {
			writeError(w, "Access denied", 403)
			return
		}
		next(w, r)
	}
}

func apiBlockIP() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IP       string `json:"ip"`
			Reason   string `json:"reason"`
			Duration int    `json:"duration"`
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
		// duration < 0 silently creates a permanent block; reject it explicitly.
		// duration == 0 is allowed (= permanent).
		if body.Duration < 0 {
			writeError(w, "Duration must be >= 0 (0 = permanent)", 400)
			return
		}
		dur := time.Duration(body.Duration) * time.Second
		ipBlocker.add(body.IP, body.Reason, dur)
		ipBlocker.save()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiUnblockIP() http.HandlerFunc {
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
		ipBlocker.remove(body.IP)
		ipBlocker.save()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiBlockedIPs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"entries": ipBlocker.list()})
	}
}

func init() {
	ipBlocker.load()
}
