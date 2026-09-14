package main

import (
	"admin/internal/platform"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetEnvSeesRuntimeSetenv(t *testing.T) {
	key := "WORKWORK_TEST_RUNTIME_ENV"
	old, hadOld := os.LookupEnv(key)
	t.Cleanup(func() {
		if hadOld {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})

	os.Unsetenv(key)
	if got := platform.GetEnv(key); got != "" {
		t.Fatalf("expected empty env before runtime set, got %q", got)
	}
	os.Setenv(key, "runtime-token")
	if got := platform.GetEnv(key); got != "runtime-token" {
		t.Fatalf("expected runtime env value, got %q", got)
	}
}

func TestDefaultTrustedProxyDoesNotTrustDockerBridgeCIDR(t *testing.T) {
	useDefaultProxyTrust(t)
	if isTrustedProxy("172.20.0.10:53122") {
		t.Fatal("default trusted proxy set must not trust the whole Docker bridge")
	}
}

func TestClientIPOnlyTrustsForwardedHeadersFromTrustedProxy(t *testing.T) {
	useDefaultProxyTrust(t)
	untrusted := newRequestWithRemoteAddr("203.0.113.10:53122")
	untrusted.Header.Set("X-Real-IP", "198.51.100.7")
	if got := clientIP(untrusted); got != "203.0.113.10" {
		t.Fatalf("untrusted peer should not control client IP, got %q", got)
	}

	trusted := newRequestWithRemoteAddr("127.0.0.1:53122")
	trusted.Header.Set("X-Real-IP", "198.51.100.7")
	if got := clientIP(trusted); got != "198.51.100.7" {
		t.Fatalf("trusted proxy header not used, got %q", got)
	}
}

func TestIPBlockStoreLoadPopulatesEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked-ips.json")
	if err := os.WriteFile(path, []byte(`{"entries":[{"ip":"198.51.100.9","reason":"test","blocked_at":"2026-01-01T00:00:00Z"}]}`), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	store := &ipBlockStore{path: path}
	store.load()
	if !store.isBlocked("198.51.100.9") {
		t.Fatal("expected loaded IP to be blocked")
	}
}

func TestFail2BanTokenOnlyAuthorizesBlockEndpoints(t *testing.T) {
	setFail2BanToken("fail2ban-secret")
	t.Cleanup(func() { setFail2BanToken("") })

	req := httptest.NewRequest(http.MethodPost, "http://admin/api/admin/security/block", nil)
	req.Header.Set("X-Fail2Ban-Token", "fail2ban-secret")
	if !isFail2BanRequest(req) || !isPrivilegedServiceRequest(req) {
		t.Fatal("expected fail2ban token to authorize block endpoint")
	}

	other := httptest.NewRequest(http.MethodPost, "http://admin/api/admin/users/admin/add", nil)
	other.Header.Set("X-Fail2Ban-Token", "fail2ban-secret")
	if isFail2BanRequest(other) || isPrivilegedServiceRequest(other) {
		t.Fatal("fail2ban token must not authorize unrelated admin endpoints")
	}
}

func newRequestWithRemoteAddr(remoteAddr string) *http.Request {
	req := httptest.NewRequest("GET", "http://example.test/", nil)
	req.RemoteAddr = remoteAddr
	return req
}

func useDefaultProxyTrust(t *testing.T) {
	t.Helper()
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.0/8,::1/128")
	t.Setenv("TRUSTED_PROXY_HOSTS", "none")
	trustedProxyHostCache.Lock()
	trustedProxyHostCache.ips = nil
	trustedProxyHostCache.expires = time.Time{}
	trustedProxyHostCache.Unlock()
}
