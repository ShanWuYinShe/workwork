package auth

import (
	"admin/internal/identity"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequireRoleTrustedProxy 覆盖此前完全没有测试的认证主链路：
// 可信代理 + X-Forwarded-User → users.json 查找 → 角色比较 → ctx actor。
func TestRequireRoleTrustedProxy(t *testing.T) {
	useDefaultProxyTrust(t)
	mw := newTestMiddleware(t)
	if _, err := mw.users.UpsertUser("alice", "password123", "viewer"); err != nil {
		t.Fatalf("seed alice: %v", err)
	}
	if _, err := mw.users.UpsertUser("rootadmin", "password123", "admin"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	call := func(role, remote, user, internalToken string) (*httptest.ResponseRecorder, *identity.UserInfo, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://admin/api/admin/dashboard", nil)
		req.RemoteAddr = remote
		if user != "" {
			req.Header.Set("X-Forwarded-User", user)
		}
		if internalToken != "" {
			req.Header.Set("X-Internal-Token", internalToken)
		}
		rec := httptest.NewRecorder()
		var info *identity.UserInfo
		var gotUser string
		mw.RequireRole(role)(func(w http.ResponseWriter, r *http.Request) {
			info, gotUser = GetUserFromContext(r)
			w.WriteHeader(http.StatusOK)
		})(rec, req)
		return rec, info, gotUser
	}

	if rec, info, user := call("viewer", "127.0.0.1:1234", "alice", ""); rec.Code != http.StatusOK || user != "alice" || info == nil || info.Role != "viewer" {
		t.Fatalf("viewer self: code=%d user=%q info=%+v", rec.Code, user, info)
	}
	if rec, _, _ := call("admin", "127.0.0.1:1234", "alice", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer on admin route: code=%d, want 403", rec.Code)
	}
	if rec, _, _ := call("admin", "127.0.0.1:1234", "rootadmin", ""); rec.Code != http.StatusOK {
		t.Fatalf("admin on admin route: code=%d, want 200", rec.Code)
	}
	if rec, _, _ := call("viewer", "127.0.0.1:1234", "nobody", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("unprovisioned user: code=%d, want 403", rec.Code)
	}
	if rec, _, _ := call("viewer", "203.0.113.10:1234", "alice", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("untrusted peer: code=%d, want 401", rec.Code)
	}
}

func TestRequireRoleInternalToken(t *testing.T) {
	mw := useInternalToken(t)
	req := httptest.NewRequest(http.MethodGet, "http://admin/api/admin/dashboard", nil)
	req.Header.Set("X-Internal-Token", "internal-secret")
	rec := httptest.NewRecorder()
	var info *identity.UserInfo
	var user string
	mw.RequireRole("admin")(func(w http.ResponseWriter, r *http.Request) {
		info, user = GetUserFromContext(r)
		w.WriteHeader(http.StatusOK)
	})(rec, req)
	if rec.Code != http.StatusOK || user != "internal" || info == nil || info.Role != "admin" {
		t.Fatalf("internal token: code=%d user=%q info=%+v", rec.Code, user, info)
	}
}

func TestRequireRoleFail2BanTokenOnlyOnBlockEndpoints(t *testing.T) {
	mw := newTestMiddleware(t)
	t.Setenv("FAIL2BAN_TOKEN", "fail2ban-secret")
	if err := mw.secrets.EnsureFail2BanToken(); err != nil {
		t.Fatal(err)
	}
	mkReq := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "http://admin"+path, nil)
		req.Header.Set("X-Fail2Ban-Token", "fail2ban-secret")
		return req
	}
	rec := httptest.NewRecorder()
	mw.RequireRole("admin")(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })(rec, mkReq("/api/admin/security/block"))
	if rec.Code != http.StatusOK {
		t.Fatalf("fail2ban block: code=%d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	mw.RequireRole("admin")(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })(rec, mkReq("/api/admin/users/admin/add"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("fail2ban on unrelated endpoint: code=%d, want 401", rec.Code)
	}
}
