package auth

import (
	"admin/internal/platform"
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

type UsersMetaDoc struct {
	Users map[string]UserInfo `json:"users"`
}

var (
	usersMetaPath = "/app/data/users.json"
	usersMetaMu   sync.Mutex
)

// ErrLastAdmin 表示"不能移除最后一个管理员"的业务错误。它由 usersMeta 持锁回调
// 返回(在 UpdateUsersMeta 的单次锁获取内完成 read-check-write),handler 层用
// errors.Is 识别并映射为 400——把 last-admin 守卫从"先读后写"的两段式改成原子
// 操作,消除并发降级/删除绕过守卫把管理员清零的 TOCTOU 窗口。
var ErrLastAdmin = errors.New("cannot remove the last admin user")

// ErrProtectedAdmin 表示"不能降级受保护的管理员账号"(AUTH_USER)。
var ErrProtectedAdmin = errors.New("cannot demote the default admin account")

// internalToken/fail2banToken 由 deploy.go 生成并加载。
// 避免使用 os.Setenv 注入进程环境（并发不安全）以及 platform.GetEnv 的双重来源行为。
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

func ReadUsersMeta() (*UsersMetaDoc, error) {
	usersMetaMu.Lock()
	defer usersMetaMu.Unlock()

	doc := &UsersMetaDoc{Users: make(map[string]UserInfo)}
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

func UpdateUsersMeta(fn func(doc *UsersMetaDoc) (bool, error)) error {
	usersMetaMu.Lock()
	defer usersMetaMu.Unlock()

	doc := &UsersMetaDoc{Users: make(map[string]UserInfo)}
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
	return platform.AtomicWriteFile(usersMetaPath, data, 0600)
}

func EnsureUserMeta(username string) (*UserInfo, error) {
	doc, err := ReadUsersMeta()
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

func IsProtectedAdmin(username string) bool {
	return username == platform.GetEnv("AUTH_USER")
}

func UpdateUserMeta(username string, info UserInfo) error {
	return UpdateUsersMeta(func(doc *UsersMetaDoc) (bool, error) {
		doc.Users[username] = info
		return true, nil
	})
}

func DeleteUserMeta(username string) error {
	return UpdateUsersMeta(func(doc *UsersMetaDoc) (bool, error) {
		delete(doc.Users, username)
		return true, nil
	})
}

func GetUserFromContext(r *http.Request) (*UserInfo, string) {
	info, _ := r.Context().Value(userInfoCtxKey).(*UserInfo)
	username, _ := r.Context().Value(usernameCtxKey).(string)
	return info, username
}

func GetOrCreateProxyPassword(username string) (string, error) {
	var password string
	created := false
	err := UpdateUsersMeta(func(doc *UsersMetaDoc) (bool, error) {
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
		if err := EnsureProxyHtpasswd(); err != nil {
			return "", err
		}
	}
	return password, nil
}

func proxyHtpasswdContainsUser(username string) bool {
	data, err := os.ReadFile(ProxyHtpasswdPath)
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
// 重写 htpasswd）。密码在用户首次添加 Xray 客户端时由 GetOrCreateProxyPassword 懒创建。
func proxyPasswordIfExists(username string) string {
	if username == "" {
		return ""
	}
	doc, err := ReadUsersMeta()
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
	raw := platform.GetEnv("TRUSTED_PROXY_CIDRS")
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
	raw := strings.TrimSpace(platform.GetEnv("TRUSTED_PROXY_HOSTS"))
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

// trustedProxyHostIPs 返回信任代理主机名解析出的 IP 集合。
// DNS 解析在锁外进行（与 cloudflare.go cfResolveZoneID 同款模式）：此前解析
// 持有全局锁，每个经 RequireRole 的请求都要过这把锁——缓存过期或 DNS 故障
// （失败只缓存 5s）期间所有请求串行排队，各付最长 200ms 解析超时，面板整体
// 延迟被放大。锁外解析 + 锁内回写（允许并发重复解析，结果幂等）消除该瓶颈。
func trustedProxyHostIPs() []net.IP {
	hosts := trustedProxyHosts()
	if len(hosts) == 0 {
		return nil
	}
	now := time.Now()
	trustedProxyHostCache.Lock()
	if now.Before(trustedProxyHostCache.expires) {
		ips := cloneIPs(trustedProxyHostCache.ips)
		trustedProxyHostCache.Unlock()
		return ips
	}
	trustedProxyHostCache.Unlock()

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
	trustedProxyHostCache.Lock()
	trustedProxyHostCache.ips = ips
	trustedProxyHostCache.expires = now.Add(ttl)
	trustedProxyHostCache.Unlock()
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

// internalTokenAllowedPrefixes 返回 INTERNAL_TOKEN_ALLOWED_PREFIXES 配置的路径
// 前缀白名单（逗号分隔）。空表示未配置——internal token 保持全权（兼容既有
// 外部自动化）；配置后 internal token 只能豁免白名单前缀内的路径，其余路径
// 一律退回正常认证（无凭证即 401），fail-closed。
func internalTokenAllowedPrefixes() []string {
	raw := strings.TrimSpace(platform.GetEnv("INTERNAL_TOKEN_ALLOWED_PREFIXES"))
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			log.Printf("WARNING: ignoring bad INTERNAL_TOKEN_ALLOWED_PREFIXES entry %q (must start with /)", p)
			continue
		}
		out = append(out, p)
	}
	return out
}

// internalTokenPrefixMatch 判断路径是否落在前缀白名单内。前缀按路径段边界
// 匹配："/api/admin/security" 命中 "/api/admin/security/block" 与自身，但不命中
// "/api/admin/security-evil"。
func internalTokenPrefixMatch(path string, prefixes []string) bool {
	for _, p := range prefixes {
		base := strings.TrimSuffix(p, "/")
		if path == base || strings.HasPrefix(path, base+"/") {
			return true
		}
	}
	return false
}

func isInternalRequest(r *http.Request) bool {
	tok := getInternalToken()
	if tok == "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(tok)) != 1 {
		return false
	}
	prefixes := internalTokenAllowedPrefixes()
	if len(prefixes) == 0 {
		return true
	}
	if internalTokenPrefixMatch(r.URL.Path, prefixes) {
		return true
	}
	// 拒绝必须留痕：运维据此发现外部自动化实际需要的最小前缀集合，
	// 再把它们配进 INTERNAL_TOKEN_ALLOWED_PREFIXES。
	log.Printf("[security] internal-token rejected: path %s not in INTERNAL_TOKEN_ALLOWED_PREFIXES", r.URL.Path)
	return false
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

func RequireRole(minRole string) func(http.HandlerFunc) http.HandlerFunc {
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
					// 伪造尝试单独留痕(带头才记,避免裸端口扫描噪音):
					// internal_docker 上的第三方容器持续探测身份伪造时,
					// 运维能像 internal-token 白名单拒绝一样从日志察觉。
					if r.Header.Get("X-Forwarded-User") != "" {
						log.Printf("[security] x-forwarded-user spoof attempt ip=%s path=%s", remoteIP(r.RemoteAddr), r.URL.Path)
					}
					platform.WriteError(w, "未认证", 401)
					return
				}
				username = r.Header.Get("X-Forwarded-User")
				if username == "" {
					platform.WriteError(w, "未认证", 401)
					return
				}
				var err error
				info, err = EnsureUserMeta(username)
				if err != nil {
					platform.WriteInternalError(w, err)
					return
				}
				if info == nil {
					// User authenticated by nginx but not provisioned in
					// users.json. Refuse to auto-create on first request —
					// admin must add them via /api/admin/users/admin/add.
					platform.WriteError(w, "用户未初始化", 403)
					return
				}
			}

			requiredLevel, ok := roleLevel[minRole]
			if !ok {
				log.Printf("invalid required role %q", minRole)
				platform.WriteError(w, "内部错误", 500)
				return
			}
			userLevel, ok := roleLevel[info.Role]
			if !ok {
				userLevel = 0
			}
			// 认证成功即落审计身份（含 403 Forbidden 的请求）：外层
			// RequestLogMiddleware 在 next 返回后读取同一 holder，把 user
			// 写进访问日志——回答"谁在何时调用了什么"。
			markAuditUser(r.Context(), username)
			if userLevel < requiredLevel {
				platform.WriteError(w, "无权限", 403)
				return
			}

			ctx := context.WithValue(r.Context(), userInfoCtxKey, info)
			ctx = context.WithValue(ctx, usernameCtxKey, username)
			next(w, r.WithContext(ctx))
		}
	}
}

// WithTestUser 供外部包测试向请求 ctx 注入已认证用户(生产代码不得调用;
// 生产路径由 requireRole 中间件写入)。
func WithTestUser(r *http.Request, info *UserInfo, username string) *http.Request {
	ctx := context.WithValue(r.Context(), userInfoCtxKey, info)
	ctx = context.WithValue(ctx, usernameCtxKey, username)
	return r.WithContext(ctx)
}
