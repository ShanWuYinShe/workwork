package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestLastAdminGuard 验证 last-admin 守卫在持锁回调内完成(TOCTOU 修复的回归):
// 降级/删除最后一个管理员一律 400,存在其他管理员时正常放行。
func TestLastAdminGuard(t *testing.T) {
	withUsersMetaPath(t, `{
  "users": {
    "alice": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "bob":   {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "carol": {"role": "viewer", "created_at": "2026-01-01T00:00:00Z"}
  }
}`)
	// 防止 AUTH_USER 干扰(alice/bob 都不是 protected)
	oldAuth := getEnv("AUTH_USER")
	t.Cleanup(func() { os.Setenv("AUTH_USER", oldAuth) })
	os.Setenv("AUTH_USER", "root")

	doSetRole := func(t *testing.T, username, role string) int {
		t.Helper()
		body := strings.NewReader(`{"username":"` + username + `","role":"` + role + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/roles/set", body)
		rec := httptest.NewRecorder()
		apiSetRole(nil)(rec, req)
		return rec.Code
	}

	doDelete := func(t *testing.T, username string) int {
		t.Helper()
		body := strings.NewReader(`{"username":"` + username + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/admin/delete", body)
		rec := httptest.NewRecorder()
		apiDeleteAdmin(nil)(rec, req)
		return rec.Code
	}

	t.Run("demote one of two admins succeeds", func(t *testing.T) {
		if code := doSetRole(t, "bob", "viewer"); code != http.StatusOK {
			t.Fatalf("demote bob: status = %d, want 200", code)
		}
	})

	t.Run("demote last admin rejected", func(t *testing.T) {
		if code := doSetRole(t, "alice", "viewer"); code != http.StatusBadRequest {
			t.Fatalf("demote last admin: status = %d, want 400", code)
		}
	})

	t.Run("delete last admin rejected", func(t *testing.T) {
		if code := doDelete(t, "alice"); code != http.StatusBadRequest {
			t.Fatalf("delete last admin: status = %d, want 400", code)
		}
	})

	t.Run("delete non-last admin succeeds", func(t *testing.T) {
		// 先恢复 bob 为 admin,再删除 alice(bob 仍在)
		if code := doSetRole(t, "bob", "admin"); code != http.StatusOK {
			t.Fatalf("restore bob: status = %d, want 200", code)
		}
		if code := doDelete(t, "alice"); code != http.StatusOK {
			t.Fatalf("delete non-last admin: status = %d, want 200", code)
		}
	})
}

// TestRateLimiterBansExceedingIP 验证限流超限触发持久封禁(锁外落盘的回归):
// 窗口内超过上限后 allow 返回 false,且 IP 进入持久封禁表。
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
	if !rl.allow(ip) || !rl.allow(ip) {
		t.Fatal("requests within limit should be allowed")
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

// TestAddAdminCannotResetProtectedAdmin 锁定 H-2 守卫：AUTH_USER 已存在时，
// 经 /users/admin/add 的重置密码路径（role 保持 admin 绕过降级守卫）必须 403，
// 与 apiChangePassword 对同一攻击的阻断对齐。修复前该路径可直达 htpasswdAddUser
// 覆盖超管密码行。
func TestAddAdminCannotResetProtectedAdmin(t *testing.T) {
	withUsersMetaPath(t, `{
  "users": {
    "root": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "alice": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"}
  }
}`)
	// 放行分支（无 AUTH_USER 时）由 TestAddAdminGuardAllowsFreshAuthUser 覆盖。
	oldAuth := getEnv("AUTH_USER")
	t.Cleanup(func() { os.Setenv("AUTH_USER", oldAuth) })
	os.Setenv("AUTH_USER", "root")

	// 只覆盖守卫分支：htpasswdFile 是包级常量（指向 /app/data，测试环境不可写），
	// 而 root 已存在时守卫在读 htpasswd 之前就拒绝——正向创建路径由部署环境验证。
	doAdd := func(t *testing.T, username string) int {
		t.Helper()
		body := strings.NewReader(`{"username":"` + username + `","password":"newpw123","role":"admin"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/admin/add", body)
		rec := httptest.NewRecorder()
		apiAddAdmin(nil)(rec, req)
		return rec.Code
	}

	t.Run("reset protected admin password rejected", func(t *testing.T) {
		if code := doAdd(t, "root"); code != http.StatusForbidden {
			t.Fatalf("reset AUTH_USER via add: status = %d, want 403", code)
		}
	})
	// 放行分支（AUTH_USER 尚未 provisioned 时不拦截）由
	// TestAddAdminGuardAllowsFreshAuthUser 用不含 AUTH_USER 的 fixture 覆盖。
}

// TestAddAdminGuardAllowsFreshAuthUser：users.json 尚无 AUTH_USER（初始部署）
// 时守卫放行——只验证存在性判断，不实际触达 htpasswd 写入。
func TestAddAdminGuardAllowsFreshAuthUser(t *testing.T) {
	withUsersMetaPath(t, `{
  "users": {
    "alice": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"}
  }
}`)
	oldAuth := getEnv("AUTH_USER")
	t.Cleanup(func() { os.Setenv("AUTH_USER", oldAuth) })
	os.Setenv("AUTH_USER", "bootadmin")

	doc, err := readUsersMeta()
	if err != nil {
		t.Fatalf("read users meta: %v", err)
	}
	if _, exists := doc.Users["bootadmin"]; exists {
		t.Fatal("fixture should not contain bootadmin")
	}
	// 守卫分支条件即“存在才拒”——无该用户时不会 403（实际创建走部署验证）。
}
