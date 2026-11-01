package auth

import (
	"admin/internal/requestctx"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequireRoleTrustedProxy 覆盖认证主链路：可信代理 + X-Forwarded-User
// → users.json 查找 → 角色比较 → requestctx actor。
func TestRequireRoleTrustedProxy(t *testing.T) {
	useDefaultProxyTrust(t)
	mw := newTestMiddleware(t)
	if _, err := mw.users.UpsertUser("alice", "password123", "viewer"); err != nil {
		t.Fatalf("seed alice: %v", err)
	}
	if _, err := mw.users.UpsertUser("rootadmin", "password123", "admin"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	call := func(role, remote, user, internalToken string) (int, requestctx.Actor, bool) {
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
		var actor requestctx.Actor
		var ok bool
		mw.RequireRole(role)(func(w http.ResponseWriter, r *http.Request) {
			actor, ok = requestctx.ActorFrom(r.Context())
			w.WriteHeader(http.StatusOK)
		})(rec, req)
		return rec.Code, actor, ok
	}

	if code, actor, ok := call("viewer", "127.0.0.1:1234", "alice", ""); code != http.StatusOK || !ok || actor.Username != "alice" || actor.Role != "viewer" {
		t.Fatalf("viewer self: code=%d actor=%+v ok=%v", code, actor, ok)
	}
	if code, _, _ := call("admin", "127.0.0.1:1234", "alice", ""); code != http.StatusForbidden {
		t.Fatalf("viewer on admin route: code=%d, want 403", code)
	}
	if code, actor, ok := call("admin", "127.0.0.1:1234", "rootadmin", ""); code != http.StatusOK || !ok || actor.Role != "admin" {
		t.Fatalf("admin on admin route: code=%d actor=%+v ok=%v", code, actor, ok)
	}
	if code, _, _ := call("viewer", "127.0.0.1:1234", "nobody", ""); code != http.StatusForbidden {
		t.Fatalf("unprovisioned user: code=%d, want 403", code)
	}
	if code, _, _ := call("viewer", "203.0.113.10:1234", "alice", ""); code != http.StatusUnauthorized {
		t.Fatalf("untrusted peer: code=%d, want 401", code)
	}
}

func TestRequireRoleInternalToken(t *testing.T) {
	mw := useInternalToken(t)
	req := httptest.NewRequest(http.MethodGet, "http://admin/api/admin/dashboard", nil)
	req.Header.Set("X-Internal-Token", "internal-secret")
	rec := httptest.NewRecorder()
	var actor requestctx.Actor
	var ok bool
	mw.RequireRole("admin")(func(w http.ResponseWriter, r *http.Request) {
		actor, ok = requestctx.ActorFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})(rec, req)
	if rec.Code != http.StatusOK || !ok || actor.Username != "internal" || actor.Role != "admin" || !actor.Internal {
		t.Fatalf("internal token: code=%d actor=%+v ok=%v", rec.Code, actor, ok)
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
