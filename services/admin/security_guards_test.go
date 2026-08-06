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
