package accounts

import (
	"admin/internal/identity"
	"admin/internal/runtime"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// newUsersStore 构造隔离的 identity store；fixture 直接写 users.json。
// 策略显式指定 protected=root，不依赖进程 env。
func newUsersStore(t *testing.T, fixture string) *identity.Store {
	t.Helper()
	layout := runtime.NewLayout(t.TempDir(), t.TempDir())
	if fixture != "" {
		if err := os.WriteFile(layout.UsersMetaPath(), []byte(fixture), 0600); err != nil {
			t.Fatalf("write users fixture: %v", err)
		}
	}
	return identity.NewStoreWithPolicy(layout, nil, identity.Policy{ProtectedUser: "root"})
}

func TestLastAdminGuard(t *testing.T) {
	users := newUsersStore(t, `{
  "users": {
    "alice": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "bob":   {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "carol": {"role": "viewer", "created_at": "2026-01-01T00:00:00Z"}
  }
}`)

	doSetRole := func(t *testing.T, username, role string) int {
		t.Helper()
		body := strings.NewReader(`{"username":"` + username + `","role":"` + role + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/roles/set", body)
		rec := httptest.NewRecorder()
		ApiSetRole(users)(rec, req)
		return rec.Code
	}

	doDelete := func(t *testing.T, username string) int {
		t.Helper()
		body := strings.NewReader(`{"username":"` + username + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/admin/delete", body)
		rec := httptest.NewRecorder()
		ApiDeleteAdmin(users, nil)(rec, req)
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
	users := newUsersStore(t, `{
  "users": {
    "root": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"},
    "alice": {"role": "admin", "created_at": "2026-01-01T00:00:00Z"}
  }
}`)

	body := strings.NewReader(`{"username":"root","password":"newpw123","role":"admin"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/admin/add", body)
	rec := httptest.NewRecorder()
	ApiAddAdmin(users)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reset AUTH_USER via add: status = %d, want 403", rec.Code)
	}
}
