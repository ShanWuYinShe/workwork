package auth

import (
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
