package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ctxKey string

const userInfoCtxKey ctxKey = "userInfo"
const usernameCtxKey ctxKey = "username"

type UserInfo struct {
	Role          string `json:"role"`
	CreatedAt     string `json:"created_at"`
	ProxyPassword string `json:"proxy_password"` // plaintext, for xhttp client config
}

type usersMetaDoc struct {
	Users map[string]UserInfo `json:"users"`
}

var (
	usersMetaPath = "/app/data/users.json"
	usersMetaMu   sync.Mutex
)

// errLastAdmin 表示"不能移除最后一个管理员"的业务错误。它由 usersMeta 持锁回调
// 返回(在 updateUsersMeta 的单次锁获取内完成 read-check-write),handler 层用
// errors.Is 识别并映射为 400——把 last-admin 守卫从"先读后写"的两段式改成原子
// 操作,消除并发降级/删除绕过守卫把管理员清零的 TOCTOU 窗口。
var errLastAdmin = errors.New("cannot remove the last admin user")

// errProtectedAdmin 表示"不能降级受保护的管理员账号"(AUTH_USER)。
var errProtectedAdmin = errors.New("cannot demote the default admin account")

// internalToken/fail2banToken 由 deploy.go 生成并加载。
// 避免使用 os.Setenv 注入进程环境（并发不安全）以及 getEnv 的双重来源行为。
var internalToken atomic.Value // holds string
var fail2banToken atomic.Value // holds string

func getInternalToken() string {
	v, _ := internalToken.Load().(string)
	return v
}

func setInternalToken(tok string) {
	internalToken.Store(tok)
}

func getFail2BanToken() string {
	v, _ := fail2banToken.Load().(string)
	return v
}

func setFail2BanToken(tok string) {
	fail2banToken.Store(tok)
}

const (
	trustedProxyHostCacheTTL     = 30 * time.Second
	trustedProxyHostFailureCache = 5 * time.Second
)

var trustedProxyHostCache = struct {
	sync.Mutex
	ips     []net.IP
	expires time.Time
}{}

// trustedProxyCIDRCache caches the parsed CIDR set. Re-parse only when the
// TRUSTED_PROXY_CIDRS env value changes (e.g. after env reload).
var trustedProxyCIDRCache = struct {
	sync.Mutex
	nets  []*net.IPNet
	raw   string
	valid bool
}{}

func readUsersMeta() (*usersMetaDoc, error) {
	usersMetaMu.Lock()
	defer usersMetaMu.Unlock()

	doc := &usersMetaDoc{Users: make(map[string]UserInfo)}
	data, err := os.ReadFile(usersMetaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, doc); err != nil {
		return nil, err
	}
	if doc.Users == nil {
		doc.Users = make(map[string]UserInfo)
	}
	return doc, nil
}

func updateUsersMeta(fn func(doc *usersMetaDoc) (bool, error)) error {
	usersMetaMu.Lock()
	defer usersMetaMu.Unlock()

	doc := &usersMetaDoc{Users: make(map[string]UserInfo)}
	data, err := os.ReadFile(usersMetaPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else if err := json.Unmarshal(data, doc); err != nil {
		return err
	}
	if doc.Users == nil {
		doc.Users = make(map[string]UserInfo)
	}

	dirty, err := fn(doc)
	if err != nil || !dirty {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(usersMetaPath), 0755); err != nil {
		return err
	}
	data, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(usersMetaPath, data, 0600)
}

func ensureUserMeta(username string) (*UserInfo, error) {
	doc, err := readUsersMeta()
	if err != nil {
		return nil, err
	}
	if existing, exists := doc.Users[username]; exists {
		return &existing, nil
	}
	// User not in users.json — must be explicitly created by admin.
	// Do NOT auto-provision on first request: any request could forge X-Forwarded-User.
	return nil, nil
}

func isProtectedAdmin(username string) bool {
	return username == getEnv("AUTH_USER")
}

func updateUserMeta(username string, info UserInfo) error {
	return updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
		doc.Users[username] = info
		return true, nil
	})
}

func deleteUserMeta(username string) error {
	return updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
		delete(doc.Users, username)
		return true, nil
	})
}

func getUserFromContext(r *http.Request) (*UserInfo, string) {
	info, _ := r.Context().Value(userInfoCtxKey).(*UserInfo)
	username, _ := r.Context().Value(usernameCtxKey).(string)
	return info, username
}

func getOrCreateProxyPassword(username string) (string, error) {
	var password string
	created := false
	err := updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
		info := doc.Users[username]
		if info.ProxyPassword != "" {
			password = info.ProxyPassword
			return false, nil
		}
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return false, err
		}
		info.ProxyPassword = hex.EncodeToString(b)
		if info.CreatedAt == "" {
			info.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		doc.Users[username] = info
		password = info.ProxyPassword
		created = true
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if created || !proxyHtpasswdContainsUser(username) {
		if err := genProxyHtpasswd(); err != nil {
			return "", err
		}
	}
	return password, nil
}

func proxyHtpasswdContainsUser(username string) bool {
	data, err := os.ReadFile(proxyHtpasswdPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("WARNING: read proxy htpasswd: %v", err)
		}
		return false
	}
	prefix := username + ":"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// proxyPasswordIfExists 返回已存在的 proxy 密码（只读，不创建）。
// 用于 GET /api/admin/users 这类读端点——避免在查询时产生副作用（生成密码 +
// 重写 htpasswd）。密码在用户首次添加 Xray 客户端时由 getOrCreateProxyPassword 懒创建。
func proxyPasswordIfExists(username string) string {
	if username == "" {
		return ""
	}
	doc, err := readUsersMeta()
	if err != nil {
		return ""
	}
	if info, ok := doc.Users[username]; ok {
		return info.ProxyPassword
	}
	return ""
}

// trustedProxyCIDRs returns the set of networks allowed to forge X-Forwarded-User.
// Configurable via TRUSTED_PROXY_CIDRS env (comma-separated CIDRs).
// Default: loopback only. The nginx container is trusted by hostname below.
func trustedProxyCIDRs() []*net.IPNet {
	defaults := "127.0.0.0/8,::1/128"
	raw := getEnv("TRUSTED_PROXY_CIDRS")
	if raw == "" {
		raw = defaults
	}

	trustedProxyCIDRCache.Lock()
	if trustedProxyCIDRCache.valid && trustedProxyCIDRCache.raw == raw {
		nets := trustedProxyCIDRCache.nets
		trustedProxyCIDRCache.Unlock()
		return nets
	}
	trustedProxyCIDRCache.Unlock()

	var nets []*net.IPNet
	for _, c := range strings.Split(raw, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			log.Printf("WARNING: ignoring bad TRUSTED_PROXY_CIDR %q: %v", c, err)
			continue
		}
		nets = append(nets, n)
	}

	trustedProxyCIDRCache.Lock()
	trustedProxyCIDRCache.raw = raw
	trustedProxyCIDRCache.nets = nets
	trustedProxyCIDRCache.valid = true
	trustedProxyCIDRCache.Unlock()
	return nets
}

func trustedProxyHosts() []string {
	raw := strings.TrimSpace(getEnv("TRUSTED_PROXY_HOSTS"))
	if raw == "" {
		raw = "nginx"
	}
	if raw == "-" || strings.EqualFold(raw, "none") {
		return nil
	}
	var hosts []string
	for _, host := range strings.Split(raw, ",") {
		host = strings.TrimSpace(host)
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func trustedProxyHostIPs() []net.IP {
	hosts := trustedProxyHosts()
	if len(hosts) == 0 {
		return nil
	}
	now := time.Now()
	trustedProxyHostCache.Lock()
	defer trustedProxyHostCache.Unlock()
	if now.Before(trustedProxyHostCache.expires) {
		return cloneIPs(trustedProxyHostCache.ips)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var ips []net.IP
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			ips = append(ips, ip)
			continue
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ips = append(ips, addr.IP)
		}
	}
	ttl := trustedProxyHostCacheTTL
	if len(ips) == 0 {
		ttl = trustedProxyHostFailureCache
	}
	trustedProxyHostCache.ips = ips
	trustedProxyHostCache.expires = now.Add(ttl)
	return cloneIPs(ips)
}

func cloneIPs(ips []net.IP) []net.IP {
	out := make([]net.IP, len(ips))
	copy(out, ips)
	return out
}

func remoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return net.ParseIP(host)
}

// isTrustedProxy reports whether the immediate caller of this service is in
// the trusted-proxy set. Without this guard, any container on the Docker bridge
// could set X-Forwarded-User to impersonate any user.
func isTrustedProxy(remoteAddr string) bool {
	ip := remoteIP(remoteAddr)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxyCIDRs() {
		if n.Contains(ip) {
			return true
		}
	}
	for _, trustedIP := range trustedProxyHostIPs() {
		if trustedIP.Equal(ip) {
			return true
		}
	}
	return false
}

func isInternalRequest(r *http.Request) bool {
	tok := getInternalToken()
	return tok != "" &&
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(tok)) == 1
}

func isFail2BanRequest(r *http.Request) bool {
	tok := getFail2BanToken()
	return tok != "" &&
		isFail2BanEndpoint(r) &&
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Fail2Ban-Token")), []byte(tok)) == 1
}

func isFail2BanEndpoint(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/api/admin/security/block", "/api/admin/security/unblock":
		return true
	default:
		return false
	}
}

func isPrivilegedServiceRequest(r *http.Request) bool {
	return isInternalRequest(r) || isFail2BanRequest(r)
}

func requireRole(minRole string) func(http.HandlerFunc) http.HandlerFunc {
	roleLevel := map[string]int{"admin": 2, "viewer": 1}

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var info *UserInfo
			var username string

			// Internal automation uses scoped shared secrets.
			if isInternalRequest(r) {
				info = &UserInfo{Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
				username = "internal"
			} else if isFail2BanRequest(r) {
				info = &UserInfo{Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
				username = "fail2ban"
			} else {
				// External requests must come through a trusted reverse proxy
				// (nginx on the docker bridge), and the proxy must have set
				// X-Forwarded-User after authenticating with basic auth.
				if !isTrustedProxy(r.RemoteAddr) {
					writeError(w, "Unauthorized", 401)
					return
				}
				username = r.Header.Get("X-Forwarded-User")
				if username == "" {
					writeError(w, "Unauthorized", 401)
					return
				}
				var err error
				info, err = ensureUserMeta(username)
				if err != nil {
					writeInternalError(w, err)
					return
				}
				if info == nil {
					// User authenticated by nginx but not provisioned in
					// users.json. Refuse to auto-create on first request —
					// admin must add them via /api/admin/users/admin/add.
					writeError(w, "User not provisioned", 403)
					return
				}
			}

			requiredLevel, ok := roleLevel[minRole]
			if !ok {
				log.Printf("invalid required role %q", minRole)
				writeError(w, "Internal error", 500)
				return
			}
			userLevel, ok := roleLevel[info.Role]
			if !ok {
				userLevel = 0
			}
			if userLevel < requiredLevel {
				writeError(w, "Forbidden", 403)
				return
			}

			ctx := context.WithValue(r.Context(), userInfoCtxKey, info)
			ctx = context.WithValue(ctx, usernameCtxKey, username)
			next(w, r.WithContext(ctx))
		}
	}
}
