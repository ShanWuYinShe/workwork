package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIPv6PrefixKey(t *testing.T) {
	tests := []struct {
		ip     string
		want   string
		isV6OK bool
	}{
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64", true},
		{"2001:db8:1:2::ffff", "2001:db8:1:2::/64", true},
		{"198.51.100.42", "", false},        // IPv4 按完整 IP 计数
		{"::ffff:198.51.100.42", "", false}, // 4in6 同上
		{"not-an-ip", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ipv6PrefixKey(tt.ip)
		if ok != tt.isV6OK {
			t.Errorf("ipv6PrefixKey(%q) ok=%v, want %v", tt.ip, ok, tt.isV6OK)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("ipv6PrefixKey(%q) = %q, want %q", tt.ip, got, tt.want)
		}
	}
}

// TestRateLimiterPrefixAggregation 验证 /64 网段聚合:同一 /64 内换后缀
// 不重置预算——网段超限后,该网段的新 IP 首个请求即封禁;相邻 /64 不受影响。
func TestRateLimiterPrefixAggregation(t *testing.T) {
	oldBlocker := ipBlocker
	ipBlocker = &ipBlockStore{path: t.TempDir() + "/blocked-ips.json"}
	t.Cleanup(func() { ipBlocker = oldBlocker })

	rl := &rateLimiter{
		entries: make(map[string]*rateLimitEntry),
		maxReqs: 2,
		window:  10 * time.Second,
		banTime: time.Minute,
	}
	const p1 = "2001:db8:1:2::"
	// 后缀 A、B 各 1 次(合计 2 次,窗口内仍在 /64 预算内)。
	if !rl.allow(p1+"1") || !rl.allow(p1+"2") {
		t.Fatal("two suffixes, one request each, should be allowed")
	}
	// 后缀 C 的第 1 个请求就该被拒:网段预算已被 A/B 耗尽,换后缀无效。
	if rl.allow(p1 + "3") {
		t.Fatal("third suffix in same /64 should hit the aggregated threshold")
	}
	// 触发阈值的 C 已被持久封禁。
	if !ipBlocker.isBlocked(p1 + "3") {
		t.Fatal("threshold-crossing suffix should be persisted into the blocker")
	}
	// 相邻 /64 的 IP 不受影响。
	if !rl.allow("2001:db8:1:99::1") {
		t.Fatal("different /64 should have its own budget")
	}
	// 已封禁的 C 在封禁期内持续被拒。
	if rl.allow(p1 + "3") {
		t.Fatal("banned suffix should stay rejected within banTime")
	}
}

// TestRateLimitExemptsTransferGETAndHEAD 验证公开分享下载的豁免口径与
// nginx limit_req 一致:GET 与 HEAD 都不经 Go 限流(下载器探测文件头会发
// HEAD,落进管理面 20req/10s 窗口会触发 30min 误封);POST(上传)仍受限流。
func TestRateLimitExemptsTransferGETAndHEAD(t *testing.T) {
	oldBlocker := ipBlocker
	ipBlocker = &ipBlockStore{path: t.TempDir() + "/blocked-ips.json"}
	t.Cleanup(func() { ipBlocker = oldBlocker })

	rl := &rateLimiter{
		entries: make(map[string]*rateLimitEntry),
		maxReqs: 1,
		window:  10 * time.Second,
		banTime: time.Minute,
	}
	origRL := authRateLimiter
	authRateLimiter = rl
	t.Cleanup(func() { authRateLimiter = origRL })

	reached := false
	handler := RateLimitMiddleware(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	})

	req := httptest.NewRequest(http.MethodHead, "/api/transfer/abc12345", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	reached = false
	handler(httptest.NewRecorder(), req)
	if !reached {
		t.Error("HEAD /api/transfer/ should bypass Go rate limit like GET")
	}

	// 同一 IP 已把 1 次预算用尽:GET/HEAD 仍豁免,POST 被限流挡下。
	rl.allow("198.51.100.7")
	req = httptest.NewRequest(http.MethodGet, "/api/transfer/abc12345", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	reached = false
	handler(httptest.NewRecorder(), req)
	if !reached {
		t.Error("GET /api/transfer/ should bypass exhausted rate limit")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/transfer/upload", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	reached = false
	handler(httptest.NewRecorder(), req)
	if reached {
		t.Error("POST /api/transfer/upload should be rate limited")
	}
}

// TestRateLimitUnblockClearsIPv6PrefixBudget 验证解封同时清掉 /64 聚合计数:
// 只清 entries[ip] 时网段预算仍是满的,同一 /64 的下一个请求会立刻再次被封禁,
// 运维侧表现为"解封无效"。
func TestRateLimitUnblockClearsIPv6PrefixBudget(t *testing.T) {
	oldBlocker := ipBlocker
	ipBlocker = &ipBlockStore{path: t.TempDir() + "/blocked-ips.json"}
	t.Cleanup(func() { ipBlocker = oldBlocker })

	rl := &rateLimiter{
		entries: make(map[string]*rateLimitEntry),
		maxReqs: 2,
		window:  10 * time.Second,
		banTime: time.Minute,
	}
	origRL := authRateLimiter
	authRateLimiter = rl
	t.Cleanup(func() { authRateLimiter = origRL })

	const prefix = "2001:db8:1:2::"
	victim := prefix + "3"
	if !rl.allow(prefix+"1") || !rl.allow(prefix+"2") {
		t.Fatal("first two suffixes in the /64 should be allowed")
	}
	if rl.allow(victim) {
		t.Fatal("third suffix should be banned by the aggregated /64 budget")
	}

	body := strings.NewReader(`{"ip":"` + victim + `"}`)
	req := httptest.NewRequest(http.MethodPost, "http://admin/api/admin/security/rate-limit/unblock", body)
	rec := httptest.NewRecorder()
	ApiRateLimitUnblock()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unblock should succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	if !rl.allow(prefix + "4") {
		t.Fatal("after unblock the /64 budget must be reset, otherwise unblock looks ineffective")
	}
}
