package auth

// CSRF 防线:admin 认证模型是 basic auth(无 token/cookie),浏览器对同一
// protection space 的跨站表单提交会自动附带缓存的 Authorization 头,且
// enctype=text/plain 可拼出合法 JSON body——已登录管理员打开恶意页面即可被
// 跨站创建 admin/封禁 IP/停服。此前全仓无任何 Origin 校验,这里整体封死。
//
// 策略:变更类请求(非 GET/HEAD/OPTIONS)携带 Origin 头时,其 host 必须与
// 请求 Host 一致(管理面经多域名访问时 Host 恒为用户实际输入的域名,天然
// 覆盖 NGINX_DOMAINS 全部站点)。不携带 Origin 的调用方(curl、fail2ban
// token、INTERNAL_TOKEN 自动化、以及不带 Origin 的老式浏览器)放行——
// CSRF 的前提是浏览器发起,非浏览器流量无此威胁模型。Origin: null(沙箱
// iframe/data: 来源)一律拒绝。

import (
	"admin/internal/platform"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func CSRFMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			next(w, r)
			return
		}
		u, err := url.Parse(origin)
		// u.Host 为空涵盖 Origin: null 与任何非 authority 形式,统一拒绝。
		if err != nil || u.Host == "" || !originHostMatches(u.Host, r.Host) {
			log.Printf("[security] csrf origin rejected origin=%q host=%q path=%s", origin, r.Host, r.URL.Path)
			platform.WriteError(w, "已拒绝跨站请求", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// originHostMatches 比较 Origin host 与请求 Host。宽松到端口粒度:nginx 以
// $host(不含端口)回传 Host,而 Origin 保留显式端口,直接字符串比较会误拒
// 非标准端口部署;CSRF 绑定的是 DNS 名,同主机名不同端口不构成跨站提权面。
func originHostMatches(originHost, reqHost string) bool {
	if strings.EqualFold(originHost, reqHost) {
		return true
	}
	oName, _, oErr := hostName(originHost)
	rName, _, rErr := hostName(reqHost)
	return oErr == nil && rErr == nil && strings.EqualFold(oName, rName)
}

// hostName 剥离端口取主机名;Host/Origin 头里的主机名可能不带端口(默认
// 端口),net.SplitHostPort 会报错,此时整体即主机名。
func hostName(hostport string) (string, string, error) {
	if name, port, err := net.SplitHostPort(hostport); err == nil {
		return name, port, nil
	}
	return hostport, "", nil
}
