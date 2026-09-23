package auth

// CSRF 防线:admin 认证模型是 basic auth(无 token/cookie),浏览器对同一
// protection space 的跨站表单提交会自动附带缓存的 Authorization 头,且
// enctype=text/plain 可拼出合法 JSON body——已登录管理员打开恶意页面即可被
// 跨站创建 admin/封禁 IP/停服。此前全仓无任何 Origin 校验,这里整体封死。
//
// 策略:变更类请求(非 GET/HEAD/OPTIONS)携带 Origin 头时,其 host 必须
// 落在域名白名单内:XHTTP_DOMAIN 已配置(生产恒为已配置)时以它为锚——
// Host 回射校验的本质是"请求声称什么域名就信什么",未来出现兄弟域名、
// 任意 Host 回源等部署变化时回射即失效;白名单把信任锚固定到已知域名。
// XHTTP_DOMAIN 未配置时退回 Host 回射(本地/测试部署无已知域名)。
// 不携带 Origin 的调用方(curl、fail2ban token、INTERNAL_TOKEN 自动化、
// 以及不带 Origin 的老式浏览器)放行——CSRF 的前提是浏览器发起,非浏览器
// 流量无此威胁模型。Origin: null(沙箱 iframe/data: 来源)一律拒绝。

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
		if err != nil || u.Host == "" || !originAllowed(u.Host, r.Host) {
			log.Printf("[security] csrf origin rejected origin=%q host=%q path=%q", origin, r.Host, r.URL.Path)
			platform.WriteError(w, "已拒绝跨站请求", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// originAllowed 判定 Origin host 是否在白名单。XHTTP_DOMAIN 已配置时严格
// 比对白名单域名(端口不敏感,理由同下);未配置时退回与请求 Host 的回射
// 比较——回射的已知弱点见文件头,属无域名配置环境的兼容降级。
func originAllowed(originHost, reqHost string) bool {
	if domain := strings.TrimSpace(platform.GetEnv("XHTTP_DOMAIN")); domain != "" {
		oName, _, oErr := hostName(originHost)
		if oErr != nil {
			return false
		}
		return strings.EqualFold(oName, strings.TrimSuffix(domain, "."))
	}
	return originHostMatches(originHost, reqHost)
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
