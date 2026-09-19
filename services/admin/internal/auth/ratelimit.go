package auth

import (
	"admin/internal/platform"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type rateLimitEntry struct {
	timestamps []time.Time
	blockedAt  time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	// v6Prefix 按 IPv6 /64 网段聚合计数:攻击者持一个 /64 可每 5 次撞库换一个
	// 后缀,按完整 IP 计数时封禁预算退化为无限次。封禁仍落在具体 IP(nginx
	// map 只能精确匹配),但计数按 /64——网段超限后,该网段每个新 IP 首个
	// 请求即触发封禁,换后缀不再重置预算。
	v6Prefix map[string]*rateLimitEntry
	maxReqs  int           // max requests in the window
	window   time.Duration // sliding window
	banTime  time.Duration // how long to ban
	stopChan chan struct{}
	stopOnce sync.Once
}

var authRateLimiter = &rateLimiter{
	entries:  make(map[string]*rateLimitEntry),
	v6Prefix: make(map[string]*rateLimitEntry),
	maxReqs:  20,               // 20 requests
	window:   10 * time.Second, // per 10 seconds
	banTime:  30 * time.Minute, // ban for 30 minutes
}

// maxRateLimitEntries 限制内存型限流表的最大条目数。走 Cloudflare 时
// clientIP() 是真实客户端 IPv6，攻击者可用海量源地址持续制造新 key；
// 若无上限，1 分钟一次的 cleanup 跟不上写入速度，最终撑爆 256M 容器内存。
// 超过上限后新 IP 直接 429（已有条目不受影响），OOM 风险被消除。
const maxRateLimitEntries = 100000

func (rl *rateLimiter) start() {
	rl.stopChan = make(chan struct{})
	go rl.cleanupLoop()
}

// stop 幂等地关闭 cleanupLoop goroutine。sync.Once 保证多次调用（如 shutdown
// 与正常退出）不会触发 close of closed channel panic。
func (rl *rateLimiter) stop() {
	rl.stopOnce.Do(func() { close(rl.stopChan) })
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
	for _, m := range []map[string]*rateLimitEntry{rl.entries, rl.v6Prefix} {
		for ip, e := range m {
			// Remove expired entries
			if !e.blockedAt.IsZero() && now.Sub(e.blockedAt) > rl.banTime {
				delete(m, ip)
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
				delete(m, ip)
			}
		}
	}
}

// ipv6PrefixKey 把 IPv6 地址折叠到 /64 网段作为聚合计数键;非全局单播 IPv6
// (IPv4、4in6、带 zone)返回 false,调用方按完整 IP 计数。
func ipv6PrefixKey(ip string) (string, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is6() || addr.Is4In6() || addr.Zone() != "" {
		return "", false
	}
	p, err := addr.Prefix(64)
	if err != nil {
		return "", false
	}
	return p.String(), true
}

func (rl *rateLimiter) allow(ip string) bool {
	// 持久化封禁在锁外执行:ipBlocker.save() 是全量序列化 + 落盘,若放在
	// rl.mu 内,攻击者持续超限会不断触发磁盘 IO,期间所有 IP 的限流判断
	// 全部排队,放大成全局请求延迟。
	var blockAfterUnlock string

	rl.mu.Lock()
	now := time.Now()

	// /64 聚合计数键(IPv6 才有,IPv4 退化为自身)。
	countKey, isV6 := ipv6PrefixKey(ip)
	if !isV6 {
		countKey = ip
	}

	e, exists := rl.entries[ip]
	if !exists {
		// 容量上限守卫：新 IP 在表满时直接拒绝，保护内存不被无限增长。
		// 只拒绝新 key，不影响已跟踪 IP 的判定。容量按两张表合计计算,
		// /64 聚合表与 IP 表同步受控。
		if len(rl.entries)+len(rl.v6Prefix) >= maxRateLimitEntries {
			rl.mu.Unlock()
			return false
		}
		e = &rateLimitEntry{}
		rl.entries[ip] = e
	}

	// Check if currently blocked
	if !e.blockedAt.IsZero() {
		if now.Sub(e.blockedAt) < rl.banTime {
			rl.mu.Unlock()
			return false
		}
		// Ban expired, reset
		e.blockedAt = time.Time{}
		e.timestamps = nil
	}

	// /64 网段计数条目(与封禁条目分离:封禁按具体 IP,计数按网段)。
	// 惰性建表:测试可用字面量构造 rateLimiter(只初始化 entries)。
	if rl.v6Prefix == nil {
		rl.v6Prefix = make(map[string]*rateLimitEntry)
	}
	ce, exists := rl.v6Prefix[countKey]
	if !exists {
		ce = &rateLimitEntry{}
		rl.v6Prefix[countKey] = ce
	}
	// /64 超限 = 整个网段预算耗尽:当前这个新 IP 直接封禁(换后缀无效)。
	// 旧的封禁判定在下面对 e.timestamps 的阈值检查保持不变。
	cutoff := now.Add(-rl.window)
	pruneTimestamps(ce, cutoff)
	if isV6 && len(ce.timestamps) >= rl.maxReqs {
		e.blockedAt = now
		blockAfterUnlock = ip
		rl.mu.Unlock()
		ipBlocker.add(blockAfterUnlock, "rate limit exceeded ("+countKey+")", rl.banTime)
		ipBlocker.save()
		platform.NotifyWebhook("ratelimit.ban", "限流自动封禁: "+ip+" (网段 "+countKey+" 超限)", map[string]string{
			"ip": ip, "prefix": countKey, "duration": rl.banTime.String(),
		})
		return false
	}

	// Prune old timestamps
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
		blockAfterUnlock = ip
		rl.mu.Unlock()
		// Also add to persistent IP blocker (outside the lock)
		ipBlocker.add(blockAfterUnlock, "rate limit exceeded", rl.banTime)
		ipBlocker.save()
		platform.NotifyWebhook("ratelimit.ban", "限流自动封禁: "+ip, map[string]string{
			"ip": ip, "duration": rl.banTime.String(),
		})
		return false
	}

	// Record this request
	e.timestamps = append(e.timestamps, now)
	ce.timestamps = append(ce.timestamps, now)
	rl.mu.Unlock()
	return true
}

func pruneTimestamps(e *rateLimitEntry, cutoff time.Time) {
	var kept []time.Time
	for _, t := range e.timestamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.timestamps = kept
}

// RateLimitMiddleware should be placed before IPBlockMiddleware so repeated
// abusive requests can be persisted into the IP block list.
func RateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Internal service requests (fail2ban etc.) bypass rate limiting
		if isPrivilegedServiceRequest(r) {
			next(w, r)
			return
		}
		// 公开分享下载不经 Go 限流:nginx 已按 IP 施加 transfer_dl(1r/s,
		// burst=5)与 transfer_conn(3 并发)约束,而多连接下载器合法场景下
		// 会突破 Go 层 20req/10s 窗口,被误封 30min 并写入持久封禁表。
		// 只放行 GET 分享下载,上传仍走管理面限流。
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/transfer/") {
			next(w, r)
			return
		}
		ip := clientIP(r)
		if ip != "" && !authRateLimiter.allow(ip) {
			platform.WriteError(w, "请求过于频繁，请稍后重试", 429)
			return
		}
		next(w, r)
	}
}

func ApiRateLimitStatus() http.HandlerFunc {
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
		platform.WriteJSON(w, map[string]any{"blocked": blocked})
	}
}

func ApiRateLimitUnblock() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IP string `json:"ip"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "请求体不是合法 JSON", 400)
			return
		}
		if body.IP == "" {
			platform.WriteError(w, "IP 必填", 400)
			return
		}
		if net.ParseIP(body.IP) == nil {
			platform.WriteError(w, "IP 地址不合法", 400)
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
		_, actor := GetUserFromContext(r)
		log.Printf("[security] rate-limit unblock ip=%s by=%s", body.IP, actor)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

// StartRateLimiter/StopRateLimiter 供 main 装配调用(包内 authRateLimiter 单例)。
func StartRateLimiter() { authRateLimiter.start() }
func StopRateLimiter()  { authRateLimiter.stop() }
