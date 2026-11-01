package main

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"net/http"
	"strings"
)

// uiDir 是镜像内的 SPA 产物目录；只在静态资源/SPA fallback 注册处出现一次。
const uiDir = "/app/ui"

// registerStaticRoutes 注册 SPA fallback 与静态资源。资源清单来自
// platform.StaticAssetPatterns，与 RequestLog 的静态识别、缓存策略共用。
func registerStaticRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/", serveSPA)
	mux.HandleFunc("GET /admin", serveSPA)
	static := http.FileServer(http.Dir(uiDir))
	for _, pattern := range platform.StaticAssetPatterns() {
		mux.Handle(pattern, static)
	}
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, uiDir+"/index.html")
}

// newHTTPHandler 组装全局安全头、静态资源缓存策略与中间件链。
// 顺序是安全语义的一部分：CSRF 必须早于限流（否则跨站伪造请求会耗尽受害者
// IP 预算并触发封禁）；IP 封禁必须早于限流（已封禁 IP 不应再消耗预算/写盘）。
func newHTTPHandler(mux *http.ServeMux, mw *auth.Middleware, bodyLimits *platform.BodyLimits) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		bodyLimits.Apply(w, r)

		if platform.IsStaticAsset(r.URL.Path) {
			// /assets/ 是指纹文件名(内容哈希)，可永久缓存；favicon/icons 非
			// 指纹，更新后需尽快生效。静态资源跳过限流（高频低价值）。
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
			mw.IPBlockMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
				mux.ServeHTTP(w2, r2)
			})(w, r)
			return
		}

		// API 与 SPA 响应禁止缓存:no-store 同时关闭磁盘缓存与 bfcache,
		// 防止登出后浏览器回退页残留管理数据(users 列表内嵌明文代理凭证)。
		w.Header().Set("Cache-Control", "no-store")
		auth.CSRFMiddleware(mw.IPBlockMiddleware(mw.RateLimitMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
			mux.ServeHTTP(w2, r2)
		})))(w, r)
	})
}
