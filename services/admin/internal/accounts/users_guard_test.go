package accounts

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"admin/internal/auth"
	"admin/internal/platform"
)

// withUsersMetaPath 覆盖 auth 的 users.json 路径到临时目录并写入初始 fixture。
func withUsersMetaPath(t *testing.T, initialJSON string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	if initialJSON != "" {
		if err := os.WriteFile(path, []byte(initialJSON), 0600); err != nil {
			t.Fatalf("write users fixture: %v", err)
		}
	}
	auth.WithUsersMetaPath(t, path)
}

func TestLastAdminGuard(t *testing.T) {
	withUsersMetaPath(t, `{
  "users": {
    "alice": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "bob":   {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "carol": {"role": "viewer", "created_at": "2026-01-01T00:00:00Z"}
  }
}`)
	// 防止 AUTH_USER 干扰(alice/bob 都不是 protected)
	oldAuth := platform.GetEnv("AUTH_USER")
	t.Cleanup(func() { os.Setenv("AUTH_USER", oldAuth) })
	os.Setenv("AUTH_USER", "root")

	doSetRole := func(t *testing.T, username, role string) int {
		t.Helper()
		body := strings.NewReader(`{"username":"` + username + `","role":"` + role + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/roles/set", body)
		rec := httptest.NewRecorder()
		ApiSetRole(nil)(rec, req)
		return rec.Code
	}

	doDelete := func(t *testing.T, username string) int {
		t.Helper()
		body := strings.NewReader(`{"username":"` + username + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/admin/delete", body)
		rec := httptest.NewRecorder()
		ApiDeleteAdmin(nil)(rec, req)
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

func TestAddAdminCannotResetProtectedAdmin(t *testing.T) {
	withUsersMetaPath(t, `{
  "users": {
    "root": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "alice": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"}
  }
}`)
	// 放行分支（无 AUTH_USER 时）由 auth.TestAddAdminGuardAllowsFreshAuthUser 覆盖。
	oldAuth := platform.GetEnv("AUTH_USER")
	t.Cleanup(func() { os.Setenv("AUTH_USER", oldAuth) })
	os.Setenv("AUTH_USER", "root")

	// 只覆盖守卫分支：HtpasswdFile 是包级常量（指向 /app/data，测试环境不可写），
	// 而 root 已存在时守卫在读 htpasswd 之前就拒绝——正向创建路径由部署环境验证。
	doAdd := func(t *testing.T, username string) int {
		t.Helper()
		body := strings.NewReader(`{"username":"` + username + `","password":"newpw123","role":"admin"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/admin/add", body)
		rec := httptest.NewRecorder()
		ApiAddAdmin(nil)(rec, req)
		return rec.Code
	}

	t.Run("reset protected admin password rejected", func(t *testing.T) {
		if code := doAdd(t, "root"); code != http.StatusForbidden {
			t.Fatalf("reset AUTH_USER via add: status = %d, want 403", code)
		}
	})
	// 放行分支（AUTH_USER 尚未 provisioned 时不拦截）由
	// auth.TestAddAdminGuardAllowsFreshAuthUser 用不含 AUTH_USER 的 fixture 覆盖。
}
