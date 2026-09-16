package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func internalTokenRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://admin"+path, nil)
	req.Header.Set("X-Internal-Token", "internal-secret")
	return req
}

func useInternalToken(t *testing.T) {
	t.Helper()
	setInternalToken("internal-secret")
	t.Cleanup(func() { setInternalToken("") })
}

// 未配置白名单时保持既有全权语义：仓库内没有 internal token 的消费者，
// 它服务于外部自动化，默认收紧会静默破坏它们。
func TestInternalTokenRemainsFullAccessWithoutWhitelist(t *testing.T) {
	useInternalToken(t)
	t.Setenv("INTERNAL_TOKEN_ALLOWED_PREFIXES", "")

	for _, p := range []string{"/api/admin/users/admin/add", "/api/admin/network/rotate-ipv6"} {
		if !isInternalRequest(internalTokenRequest(p)) {
			t.Fatalf("expected full access without whitelist, rejected on %s", p)
		}
	}
}

// 配置白名单后，前缀内放行、前缀外一律拒绝（退回正常认证，无凭证即 401）。
func TestInternalTokenWhitelistRestrictsPaths(t *testing.T) {
	useInternalToken(t)
	t.Setenv("INTERNAL_TOKEN_ALLOWED_PREFIXES", "/api/admin/security, /api/admin/dashboard")

	allowed := []string{
		"/api/admin/security",
		"/api/admin/security/block",
		"/api/admin/security/unblock",
		"/api/admin/dashboard",
	}
	for _, p := range allowed {
		if !isInternalRequest(internalTokenRequest(p)) {
			t.Fatalf("expected internal token to be allowed on %s", p)
		}
	}

	denied := []string{
		"/api/admin/users/admin/add",
		"/api/admin/network/rotate-ipv6",
		"/api/admin",               // 白名单前缀的父路径不放行
		"/api/admin/security-evil", // 段边界：相似前缀不是同一子树
	}
	for _, p := range denied {
		if isInternalRequest(internalTokenRequest(p)) {
			t.Fatalf("expected internal token to be rejected on %s", p)
		}
	}
}

func TestInternalTokenPrefixMatchBoundary(t *testing.T) {
	prefixes := []string{"/api/admin/security"}
	cases := []struct {
		path  string
		match bool
	}{
		{"/api/admin/security", true},
		{"/api/admin/security/", true},
		{"/api/admin/security/block", true},
		{"/api/admin/security-evil", false},
		{"/api/admin/securit", false},
		{"/api/admin", false},
	}
	for _, c := range cases {
		if got := internalTokenPrefixMatch(c.path, prefixes); got != c.match {
			t.Fatalf("internalTokenPrefixMatch(%q) = %v, want %v", c.path, got, c.match)
		}
	}
}

// 非法条目（缺前导斜杠）被忽略且不影响合法条目。
func TestInternalTokenWhitelistIgnoresBadEntries(t *testing.T) {
	useInternalToken(t)
	t.Setenv("INTERNAL_TOKEN_ALLOWED_PREFIXES", "api/admin/no-slash, /api/admin/dashboard")

	if !isInternalRequest(internalTokenRequest("/api/admin/dashboard")) {
		t.Fatal("expected valid entry to be honored alongside a malformed one")
	}
	if isInternalRequest(internalTokenRequest("/api/admin/no-slash")) {
		t.Fatal("malformed entry (no leading slash) must not grant access")
	}
}
