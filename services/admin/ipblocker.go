package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
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
		return
	}
	json.Unmarshal(data, &s)
}

func (s *ipBlockStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.MarshalIndent(s, "", "  ")
	os.MkdirAll("/app/data", 0755)
	os.WriteFile(s.path, b, 0644)
}

func (s *ipBlockStore) isBlocked(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.Entries {
		if e.IP == ip {
			if e.ExpiresAt != "" {
				t, err := time.Parse(time.RFC3339, e.ExpiresAt)
				if err == nil && time.Now().UTC().After(t) {
					continue
				}
			}
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

func (s *ipBlockStore) list() []ipBlockEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ipBlockEntry, len(s.Entries))
	copy(out, s.Entries)
	return out
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func ipBlockMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if ip != "" && ipBlocker.isBlocked(ip) {
			writeError(w, "Access denied", 403)
			return
		}
		next(w, r)
	}
}

func apiBlockIP(cli interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IP       string `json:"ip"`
			Reason   string `json:"reason"`
			Duration int    `json:"duration"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
			writeError(w, "IP required", 400)
			return
		}
		dur := time.Duration(body.Duration) * time.Second
		ipBlocker.add(body.IP, body.Reason, dur)
		ipBlocker.save()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiUnblockIP(cli interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ IP string `json:"ip"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
			writeError(w, "IP required", 400)
			return
		}
		ipBlocker.remove(body.IP)
		ipBlocker.save()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiBlockedIPs(cli interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"entries": ipBlocker.list()})
	}
}

func init() {
	ipBlocker.load()
}
