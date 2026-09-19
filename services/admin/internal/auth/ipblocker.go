package auth

import (
	"admin/internal/platform"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// security block API 业务错误码：前端 Security 页与 fail2ban 按此分支。
const (
	errCodeInvalidIP       = "INVALID_IP"
	errCodeInvalidJSON     = "INVALID_JSON"
	errCodeInvalidDuration = "INVALID_DURATION"
	errCodeBlockNotFound   = "BLOCK_NOT_FOUND"

	// maxPermanentBlockWarn 是永久封禁条目的告警阈值:超过后按 1000 取模
	// 周期性 WARNING,提示审视 blocked-ips.json 是否堆积了失效条目。
	maxPermanentBlockWarn = 5000
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
	// 容量告警:永久条目(无 ExpiresAt)只能手工/自动化清理,单调累积会让
	// blocked-ips.json 与 nginx map 无限膨胀(每次 reload 全量渲染)。超过
	// 阈值按 1000 取模周期性告警,提示运维审视是否存在可清退的过期条目。
	permanent := 0
	for _, e := range s.Entries {
		if !blockEntryExpired(e) && e.ExpiresAt == "" {
			permanent++
		}
	}
	if permanent > maxPermanentBlockWarn && permanent%1000 == 0 {
		log.Printf("WARNING: %d permanent blocked IPs in %s — review and unblock stale entries", permanent, s.path)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		log.Printf("ERROR: failed to marshal blocked IPs: %v", err)
		return
	}
	if err := platform.AtomicWriteFile(s.path, b, 0600); err != nil {
		log.Printf("ERROR: failed to write blocked IPs: %v", err)
	}
	// 同步到 nginx 层(去抖 + 异步):让 /proxy/query、媒体 API 等非 admin 面
	// 也执行同一封禁。只调度不阻塞——save 可能在限流热路径持锁调用。
	ScheduleBlocklistSync()
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

// find 返回指定 IP 封禁条目的副本（锁内读，nil 表示未封禁）。
// 供 ApiBlockIP 回读「add 后实际生效」的条目——add 对已封禁 IP 是覆盖
// 语义（更新 reason/时长），回读才能保证响应反映真实状态而非请求参数。
func (s *ipBlockStore) find(ip string) *ipBlockEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Entries {
		if s.Entries[i].IP == ip {
			e := s.Entries[i]
			return &e
		}
	}
	return nil
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

// forwardedForIP 从 X-Forwarded-For 链解析客户端 IP。取"最后一个可解析条目"
// 而非第一个:nginx 用 $proxy_add_x_forwarded_for 追加的 $remote_addr 在末尾,
// 是可信的一跳;链首由客户端可伪造(纵深防御——正常路径 nginx 恒设置 X-Real-IP,
// 此函数仅在 X-Real-IP 缺失时兜底)。
func forwardedForIP(raw string) net.IP {
	parts := strings.Split(raw, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if ip := headerIP(parts[i]); ip != nil {
			return ip
		}
	}
	return nil
}

func IPBlockMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Internal service calls are authenticated by shared secret and must
		// still work when fail2ban has just blocked an external client IP.
		if isPrivilegedServiceRequest(r) {
			next(w, r)
			return
		}
		ip := clientIP(r)
		if ip != "" && ipBlocker.isBlocked(ip) {
			platform.WriteError(w, "Access denied", 403)
			return
		}
		next(w, r)
	}
}

func ApiBlockIP() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IP       string `json:"ip"`
			Reason   string `json:"reason"`
			Duration int    `json:"duration"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteErrorCode(w, "Invalid JSON", 400, errCodeInvalidJSON)
			return
		}
		if body.IP == "" {
			platform.WriteErrorCode(w, "IP required", 400, errCodeInvalidIP)
			return
		}
		if net.ParseIP(body.IP) == nil {
			platform.WriteErrorCode(w, "Invalid IP address", 400, errCodeInvalidIP)
			return
		}
		// duration < 0 silently creates a permanent block; reject it explicitly.
		// duration == 0 is allowed (= permanent). Upper bound guards the
		// time.Duration multiply: values over ~292 years overflow int64 to
		// negative and would silently turn into a permanent block.
		if body.Duration < 0 || int64(body.Duration) > int64((10*365*24*time.Hour)/time.Second) {
			platform.WriteErrorCode(w, "Duration must be 0 (permanent) or at most 10 years in seconds", 400, errCodeInvalidDuration)
			return
		}
		dur := time.Duration(body.Duration) * time.Second
		ipBlocker.add(body.IP, body.Reason, dur)
		ipBlocker.save()

		// 回读生效条目：add 对已封禁 IP 是覆盖（更新 reason/时长），
		// 用回读结果构造响应，保证展示的封禁状态与持久化一致。
		// find 与 add 是两个独立临界区，并发 unblock 同一 IP（fail2ban 到期
		// 解封）时 find 可能返回 nil，不能直接解引用。
		entry := ipBlocker.find(body.IP)
		kind := "permanent"
		blockedAt, expiresAt := "", ""
		if entry != nil {
			blockedAt, expiresAt = entry.BlockedAt, entry.ExpiresAt
			if entry.ExpiresAt != "" {
				kind = "temporary"
			}
		}
		_, actor := GetUserFromContext(r) // fail2ban 触发时 actor="fail2ban"
		log.Printf("[security] block ip=%s reason=%q duration=%ds kind=%s by=%s",
			body.IP, body.Reason, body.Duration, kind, actor)
		platform.NotifyWebhook("security.block", "IP 封禁: "+body.IP, map[string]string{
			"ip": body.IP, "reason": body.Reason, "kind": kind, "by": actor,
		})

		platform.WriteJSON(w, map[string]any{
			"status": "ok",
			"block": map[string]any{
				"ip":         body.IP,
				"reason":     body.Reason,
				"blocked_at": blockedAt,
				"expires_at": expiresAt, // 空串 = 永久
				"kind":       kind,
				"duration":   body.Duration,
			},
		})
	}
}

func ApiUnblockIP() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IP string `json:"ip"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteErrorCode(w, "Invalid JSON", 400, errCodeInvalidJSON)
			return
		}
		if body.IP == "" {
			platform.WriteErrorCode(w, "IP required", 400, errCodeInvalidIP)
			return
		}
		if net.ParseIP(body.IP) == nil {
			platform.WriteErrorCode(w, "Invalid IP address", 400, errCodeInvalidIP)
			return
		}
		// 幂等提示分支：目标不在列表 → 404 + 错误码（明确语义而非静默 ok）。
		// 注意：fail2ban 的 unban 在 bantime 到期后触发，此时条目可能已被
		// cleanupExpired 清理，404 属预期，action 侧应容忍（见 admin-api.conf）。
		if prev := ipBlocker.find(body.IP); prev == nil {
			platform.WriteErrorCode(w, "IP is not blocked", http.StatusNotFound, errCodeBlockNotFound)
			return
		}
		ipBlocker.remove(body.IP)
		ipBlocker.save()
		_, actor := GetUserFromContext(r)
		log.Printf("[security] unblock ip=%s by=%s", body.IP, actor)
		platform.NotifyWebhook("security.unblock", "IP 解封: "+body.IP, map[string]string{
			"ip": body.IP, "by": actor,
		})
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiBlockedIPs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		platform.WriteJSON(w, map[string]any{"entries": ipBlocker.list()})
	}
}

func init() {
	ipBlocker.load()
}
