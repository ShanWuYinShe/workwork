package auth

import (
	"admin/internal/identity"
	"admin/internal/platform"
	"admin/internal/requestctx"
	"context"
	"crypto/subtle"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Middleware 持有认证所需的身份/密钥依赖。之前这些依赖是 auth 包级全局
// 变量（usersMetaPath、internalToken atomic.Value），任何 import auth 的
// 包都会被绑到同一份全局状态，测试只能靠 testhooks 重定向。
type Middleware struct {
	users   *identity.Store
	secrets *identity.Secrets
}

func NewMiddleware(users *identity.Store, secrets *identity.Secrets) *Middleware {
	return &Middleware{users: users, secrets: secrets}
}

const (
	trustedProxyHostCacheTTL     = 30 * time.Second
	trustedProxyHostFailureCache = 5 * time.Second
)

var trustedProxyHostCache = struct {
	sync.Mutex
	ips      []net.IP
	lastGood []net.IP
	expires  time.Time
}{}

// trustedProxyCIDRCache caches the parsed CIDR set. Re-parse only when the
// TRUSTED_PROXY_CIDRS env value changes (e.g. after env reload).
var trustedProxyCIDRCache = struct {
	sync.Mutex
	nets  []*net.IPNet
	raw   string
	valid bool
}{}

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
// DNS 解析在锁外进行：锁内解析会让缓存过期/DNS 故障期间所有请求串行排队。
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
	hardFailed := false // 多 IP 拒信属稳定状态,缓存用正常 TTL 而非故障短缓存
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			ips = append(ips, ip)
			continue
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			continue
		}
		// fail-closed:Docker 用户定义网络允许多个容器共享同一 network-alias,
		// 同网容器以 "nginx" 别名抢注后,DNS 会把攻击者 IP 与真 nginx IP 一并
		// 返回。同一主机名解析出多个不同 IP 时整体弃用该主机并留痕。
		seen := make(map[string]bool, len(addrs))
		var hostIPs []net.IP
		for _, addr := range addrs {
			key := addr.IP.String()
			if !seen[key] {
				seen[key] = true
				hostIPs = append(hostIPs, addr.IP)
			}
		}
		if len(hostIPs) > 1 {
			list := make([]string, 0, len(hostIPs))
			for _, ip := range hostIPs {
				list = append(list, ip.String())
			}
			log.Printf("[security] trusted proxy host %q resolved to multiple IPs %v; refusing to trust any of them", host, list)
			hardFailed = true
			continue
		}
		ips = append(ips, hostIPs...)
	}
	ttl := trustedProxyHostCacheTTL
	if len(ips) == 0 && !hardFailed {
		ttl = trustedProxyHostFailureCache
	}
	trustedProxyHostCache.Lock()
	if len(ips) > 0 {
		trustedProxyHostCache.lastGood = ips
	} else if !hardFailed && len(trustedProxyHostCache.lastGood) > 0 {
		log.Printf("WARNING: trusted proxy DNS resolution failed; reusing last known good IPs %v", trustedProxyHostCache.lastGood)
		ips = trustedProxyHostCache.lastGood
	}
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

// isTrustedProxy reports whether the immediate caller is in the trusted-proxy
// set. Without this guard, any container on the Docker bridge could set
// X-Forwarded-User to impersonate any user.
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

// internalTokenAllowedPrefixes 返回 INTERNAL_TOKEN_ALLOWED_PREFIXES 配置的
// 路径前缀白名单（逗号分隔）。空表示未配置——internal token 保持全权。
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

// internalTokenPrefixMatch 按路径段边界匹配前缀。
func internalTokenPrefixMatch(path string, prefixes []string) bool {
	for _, p := range prefixes {
		base := strings.TrimSuffix(p, "/")
		if path == base || strings.HasPrefix(path, base+"/") {
			return true
		}
	}
	return false
}

func (m *Middleware) isInternalRequest(r *http.Request) bool {
	tok := m.secrets.InternalToken()
	if tok == "" {
		return false
	}
	presented := r.Header.Get("X-Internal-Token")
	if presented == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			presented = strings.TrimPrefix(h, "Bearer ")
		}
	}
	if subtle.ConstantTimeCompare([]byte(presented), []byte(tok)) != 1 {
		return false
	}
	prefixes := internalTokenAllowedPrefixes()
	if len(prefixes) == 0 {
		return true
	}
	if internalTokenPrefixMatch(r.URL.Path, prefixes) {
		return true
	}
	log.Printf("[security] internal-token rejected: path %q not in INTERNAL_TOKEN_ALLOWED_PREFIXES", r.URL.Path)
	return false
}

func (m *Middleware) isFail2BanRequest(r *http.Request) bool {
	tok := m.secrets.Fail2BanToken()
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

func (m *Middleware) isPrivilegedServiceRequest(r *http.Request) bool {
	return m.isInternalRequest(r) || m.isFail2BanRequest(r)
}

func (m *Middleware) RequireRole(minRole string) func(http.HandlerFunc) http.HandlerFunc {
	roleLevel := map[string]int{"admin": 2, "viewer": 1}

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var actor requestctx.Actor

			// Internal automation uses scoped shared secrets.
			if m.isInternalRequest(r) {
				actor = requestctx.Actor{Username: "internal", Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339), Internal: true}
			} else if m.isFail2BanRequest(r) {
				actor = requestctx.Actor{Username: "fail2ban", Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339), Internal: true}
			} else {
				// External requests must come through a trusted reverse proxy
				// (nginx on the docker bridge), and the proxy must have set
				// X-Forwarded-User after authenticating with basic auth.
				if !isTrustedProxy(r.RemoteAddr) {
					if r.Header.Get("X-Forwarded-User") != "" {
						log.Printf("[security] x-forwarded-user spoof attempt ip=%s path=%q", remoteIP(r.RemoteAddr), r.URL.Path)
					}
					platform.WriteError(w, "未认证", 401)
					return
				}
				username := r.Header.Get("X-Forwarded-User")
				if username == "" {
					platform.WriteError(w, "未认证", 401)
					return
				}
				info, err := m.users.Lookup(username)
				if err != nil {
					platform.WriteInternalError(w, err)
					return
				}
				if info == nil {
					// User authenticated by nginx but not provisioned in
					// users.json. Refuse to auto-create on first request.
					platform.WriteError(w, "用户未初始化", 403)
					return
				}
				actor = requestctx.Actor{Username: username, Role: info.Role, CreatedAt: info.CreatedAt}
			}

			requiredLevel, ok := roleLevel[minRole]
			if !ok {
				log.Printf("invalid required role %q", minRole)
				platform.WriteError(w, "内部错误", 500)
				return
			}
			userLevel, ok := roleLevel[actor.Role]
			if !ok {
				userLevel = 0
			}
			// 认证成功即落审计身份（含 403 Forbidden 的请求）。
			markAuditUser(r.Context(), actor.Username)
			if userLevel < requiredLevel {
				platform.WriteError(w, "无权限", 403)
				return
			}

			ctx := requestctx.WithActor(r.Context(), actor)
			next(w, r.WithContext(ctx))
		}
	}
}
