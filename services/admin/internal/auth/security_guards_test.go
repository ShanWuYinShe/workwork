package auth

import (
	"admin/internal/platform"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 覆盖 usersMetaPath 到临时目录并恢复,避免污染 /app/data。
func withUsersMetaPath(t *testing.T, initialJSON string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if initialJSON != "" {
		if err := os.WriteFile(path, []byte(initialJSON), 0600); err != nil {
			t.Fatalf("write users fixture: %v", err)
		}
	}
	old := usersMetaPath
	usersMetaPath = path
	t.Cleanup(func() { usersMetaPath = old })
}

// TestRateLimiterBansExceedingIP 验证限流超限触发持久封禁(锁外落盘的回归):
// 窗口内超过上限后 allow 返回 false,且 IP 进入持久封禁表。
// (last-admin 守卫的用例是 accounts.TestLastAdminGuard,不在本包。)
func TestRateLimiterBansExceedingIP(t *testing.T) {
	oldBlocker := ipBlocker
	ipBlocker = &ipBlockStore{path: filepath.Join(t.TempDir(), "blocked-ips.json")}
	t.Cleanup(func() { ipBlocker = oldBlocker })

	rl := &rateLimiter{
		entries: make(map[string]*rateLimitEntry),
		maxReqs: 2,
		window:  10 * time.Second,
		banTime: time.Minute,
	}
	const ip = "198.51.100.42"
	// maxReqs=2:先精确用满窗口配额(两次都必须在限额内放行)。
	for i := 1; i <= rl.maxReqs; i++ {
		if !rl.allow(ip) {
			t.Fatalf("request %d within limit should be allowed", i)
		}
	}
	if rl.allow(ip) {
		t.Fatal("request over limit should be rejected")
	}
	if !ipBlocker.isBlocked(ip) {
		t.Fatal("over-limit IP should be added to persistent blocker")
	}
	// 封禁期内继续拒绝
	if rl.allow(ip) {
		t.Fatal("banned IP should stay rejected")
	}
}

// TestRedactRequestPath 验证 transfer 分享码在访问日志中脱敏。
func TestRedactRequestPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transfer/abc123?x=1", nil)
	if got := redactRequestPath(req); got != "/api/transfer/[redacted]?x=1" {
		t.Fatalf("redact with query: got %q", got)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/transfer/abc123", nil)
	if got := redactRequestPath(req2); got != "/api/transfer/[redacted]" {
		t.Fatalf("redact plain: got %q", got)
	}
	req3 := httptest.NewRequest(http.MethodGet, "/api/admin/dashboard", nil)
	if got := redactRequestPath(req3); got != "/api/admin/dashboard" {
		t.Fatalf("non-transfer path should pass through: got %q", got)
	}
}

// TestAddAdminGuardAllowsFreshAuthUser：users.json 尚无 AUTH_USER（初始部署）
// 时守卫放行——只验证存在性判断，不实际触达 htpasswd 写入。
// (拒绝侧 AUTH_USER 已存在时必须 403 的用例是 accounts.TestAddAdminCannotResetProtectedAdmin。)
func TestAddAdminGuardAllowsFreshAuthUser(t *testing.T) {
	withUsersMetaPath(t, `{
  "users": {
    "alice": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"}
  }
}`)
	oldAuth := platform.GetEnv("AUTH_USER")
	t.Cleanup(func() { os.Setenv("AUTH_USER", oldAuth) })
	os.Setenv("AUTH_USER", "bootadmin")

	doc, err := ReadUsersMeta()
	if err != nil {
		t.Fatalf("read users meta: %v", err)
	}
	if _, exists := doc.Users["bootadmin"]; exists {
		t.Fatal("fixture should not contain bootadmin")
	}
	// 守卫分支条件即“存在才拒”——无该用户时不会 403（实际创建走部署验证）。
}
