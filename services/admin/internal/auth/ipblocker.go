package auth

import (
	"admin/internal/platform"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
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
	// loadErr 记录启动时读取失败（非文件不存在、非损坏隔离）的错误。
	// 此时内存里的 Entries 不是磁盘现状，必须拒绝 save，否则会用空表
	// 覆盖原文件，历史封禁全部丢失（fail-open）。
	loadErr error
	// saveTimer 是 saveDebounced 的去抖定时器（调用方须持有 mu）。
	saveTimer *time.Timer
}

var ipBlocker = &ipBlockStore{path: "/app/data/blocked-ips.json"}

// FlushBlockedIPs 立即落盘待写入的封禁状态（优雅关闭时调用，避免去抖窗口
// 内的最后一批封禁丢失）。
func FlushBlockedIPs() {
	ipBlocker.flush()
}

func (s *ipBlockStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 用临时结构加载，失败时不污染内存现状。
	var loaded struct {
		Entries []ipBlockEntry `json:"entries"`
	}
	if err := platform.LoadJSONFile(s.path, &loaded); err != nil {
		if errors.Is(err, platform.ErrCorruptJSON) {
			// 损坏文件已被隔离（原字节保留在 .corrupt），从空表继续；
			// 新封禁仍要能落盘/同步到 nginx，所以不置 loadErr。
			log.Printf("WARNING: blocked-ips corrupt, starting from empty (quarantined): %v", err)
		} else {
			s.loadErr = err
			log.Printf("ERROR: blocked-ips load failed, refusing to overwrite %s until the file is readable again: %v", s.path, err)
			return
		}
	}
	s.loadErr = nil
	s.Entries = loaded.Entries
	// expires_at 不可解析的条目:blockEntryExpired 视为"未过期"(保持
	// fail-closed),cleanupExpired 又因解析失败永不清理,结果是永久占位
	// 且无任何告警。加载时归一化成显式永久封禁(空 ExpiresAt):与强制执行
	// 语义一致,管理页可见,也能人工解封。
	for i := range s.Entries {
		if s.Entries[i].ExpiresAt == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, s.Entries[i].ExpiresAt); err != nil {
			log.Printf("WARNING: blocked-ips.json entry %q has unparsable expires_at %q, treating as permanent block", s.Entries[i].IP, s.Entries[i].ExpiresAt)
			s.Entries[i].ExpiresAt = ""
		}
	}
}

func (s *ipBlockStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.saveLocked()
}

// saveDebounced 合并高频封禁写入：限流拒绝路径可能每个请求都触发封禁，
// 立即全量序列化+fsync+nginx reload 会被放大成磁盘/CPU 风暴。1s 内的多次
// 调用只落盘一次；进程关闭时用 flush() 保证最后一次写入不丢。
func (s *ipBlockStore) saveDebounced() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		log.Printf("ERROR: refusing to persist blocked IPs: initial load failed (%v); fix or remove %s", s.loadErr, s.path)
		return
	}
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	var tm *time.Timer
	tm = time.AfterFunc(time.Second, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// 只清理"自己"：如果期间又排了一个新 timer，不要把它的指针清掉。
		if s.saveTimer == tm {
			s.saveTimer = nil
		}
		s.saveLocked()
	})
	s.saveTimer = tm
}

// flush 取消待触发的去抖定时器并立即落盘，供优雅关闭调用。
func (s *ipBlockStore) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.saveLocked()
}

// saveLocked 假设调用方已持有 s.mu。
func (s *ipBlockStore) saveLocked() {
	if s.loadErr != nil {
		log.Printf("ERROR: refusing to persist blocked IPs: initial load failed (%v); fix or remove %s", s.loadErr, s.path)
		return
	}
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

// remove 删除指定 IP 的封禁条目,并返回是否真的删掉了。
// 返回值让调用方在一次持锁内完成"存在性判定 + 删除":先 find 再 remove 是
// 两段临界区,期间并发清理(cleanupExpired)会让 remove 静默变成 no-op,而
// 接口仍报 ok,与 errCodeBlockNotFound 的 404 语义相矛盾。
func (s *ipBlockStore) remove(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []ipBlockEntry
	removed := false
	for _, e := range s.Entries {
		if e.IP == ip {
			removed = true
			continue
		}
		kept = append(kept, e)
	}
	s.Entries = kept
	return removed
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

func (m *Middleware) IPBlockMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Internal service calls are authenticated by shared secret and must
		// still work when fail2ban has just blocked an external client IP.
		if m.isPrivilegedServiceRequest(r) {
			next(w, r)
			return
		}
		ip := clientIP(r)
		if ip != "" && ipBlocker.isBlocked(ip) {
			platform.WriteError(w, "访问被拒绝", 403)
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
			platform.WriteErrorCode(w, "请求体不是合法 JSON", 400, errCodeInvalidJSON)
			return
		}
		if body.IP == "" {
			platform.WriteErrorCode(w, "IP 必填", 400, errCodeInvalidIP)
			return
		}
		if net.ParseIP(body.IP) == nil {
			platform.WriteErrorCode(w, "IP 地址不合法", 400, errCodeInvalidIP)
			return
		}
		// duration < 0 silently creates a permanent block; reject it explicitly.
		// duration == 0 is allowed (= permanent). Upper bound guards the
		// time.Duration multiply: values over ~292 years overflow int64 to
		// negative and would silently turn into a permanent block.
		if body.Duration < 0 || int64(body.Duration) > int64((10*365*24*time.Hour)/time.Second) {
			platform.WriteErrorCode(w, "时长必须为 0（永久）或不超过 10 年的秒数", 400, errCodeInvalidDuration)
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
			platform.WriteErrorCode(w, "请求体不是合法 JSON", 400, errCodeInvalidJSON)
			return
		}
		if body.IP == "" {
			platform.WriteErrorCode(w, "IP 必填", 400, errCodeInvalidIP)
			return
		}
		if net.ParseIP(body.IP) == nil {
			platform.WriteErrorCode(w, "IP 地址不合法", 400, errCodeInvalidIP)
			return
		}
		// 幂等提示分支：目标不在列表 → 404 + 错误码（明确语义而非静默 ok）。
		// 注意：fail2ban 的 unban 在 bantime 到期后触发，此时条目可能已被
		// cleanupExpired 清理，404 属预期，action 侧应容忍（见 admin-api.conf）。
		if !ipBlocker.remove(body.IP) {
			platform.WriteErrorCode(w, "该 IP 未被封禁", http.StatusNotFound, errCodeBlockNotFound)
			return
		}
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
		page, ok := platform.ParseListPage(w, r)
		if !ok {
			return
		}
		platform.WriteJSON(w, map[string]any{"entries": platform.Apply(ipBlocker.list(), page)})
	}
}

func init() {
	ipBlocker.load()
}
