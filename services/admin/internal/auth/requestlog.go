package auth

import (
	"admin/internal/platform"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// RequestLogMiddleware 记录每个进入 admin 服务的 HTTP 请求的结构化访问日志。
//
// **为什么需要它**:在此之前 admin 零请求日志,完全依赖 nginx 反代日志。
// nginx 看不到应用层细节(handler 内部错误、内部令牌请求、basic-auth 之外
// 的公开下载路径),也看不到 admin 内部对 media_server /internal/* 的代理调用。
// 这里在应用层补上访问日志,与 nginx 互补,便于排查鉴权失败、RBAC 拒绝、
// handler 异常等问题。
//
// 复用 middleware.go 中已带 trusted-proxy 守卫的 clientIP(r),正确解析
// nginx 转发的真实客户端 IP,且不盲信可被伪造的 X-Forwarded-For。
//
// 记录字段:method、path、status、duration_ms、client_ip、bytes、user。
// 按 status 分级:2xx/3xx → Info,4xx → Warn,5xx → Error。
// 静态资源(/assets/ 等)高频低价值,降级为 Debug 避免刷屏。
// 不记录请求体/响应体(隐私 + 体积)。

// auditCtxKey / requestAudit 让内层 RequireRole 把解析出的用户名带给外层
// 本中间件。context 不可变,故用指针 holder:中间件在 next.ServeHTTP 返回后
// 读取同一结构——认证成功(含 403)的请求日志带 user 字段,未认证的为空,
// 回答"谁在何时调用了什么"。internal/fail2ban 以伪身份名出现在日志里,
// 与真人操作可区分。
type auditCtxKey struct{}

type requestAudit struct {
	user string
}

func markAuditUser(ctx context.Context, username string) {
	if a, ok := ctx.Value(auditCtxKey{}).(*requestAudit); ok && a != nil {
		a.user = username
	}
}

func RequestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		audit := &requestAudit{}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), auditCtxKey{}, audit)))

		duration := time.Since(start).Milliseconds()
		level := slog.LevelInfo
		switch {
		case platform.IsStaticAsset(r.URL.Path):
			level = slog.LevelDebug
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		}
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", redactRequestPath(r)),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", duration),
			slog.String("client_ip", clientIP(r)),
			slog.Int("bytes", rec.bytes),
			slog.String("user_agent", r.UserAgent()),
		}
		if audit.user != "" {
			attrs = append(attrs, slog.String("user", audit.user))
		}
		slog.Default().LogAttrs(r.Context(), level, "request", attrs...)
	})
}

// redactRequestPath 脱敏敏感路径:分享码/客户端 UUID/扫码会话 key 相当于临时
// 凭据,写进日志文件即泄露(日志可能被转发/留存);保留 query 便于排查。
func redactRequestPath(r *http.Request) string {
	path := r.URL.RequestURI()
	q := ""
	if idx := strings.Index(path, "?"); idx > 0 {
		q = path[idx:]
		path = path[:idx]
	}
	switch {
	case strings.HasPrefix(path, "/api/transfer/"):
		return "/api/transfer/[redacted]" + q
	case strings.HasPrefix(path, "/api/admin/users/xray/config/"):
		return "/api/admin/users/xray/config/[redacted]" + q
	case strings.HasPrefix(path, "/api/admin/music/netease/login/poll"):
		// poll 的 ?key= 是扫码会话标识,与二维码绑定。
		return "/api/admin/music/netease/login/poll?key=[redacted]"
	}
	return path + q
}

// statusRecorder 包裹 http.ResponseWriter 以捕获状态码与已写字节数。
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush 透传到底层 ResponseWriter,让本中间件包裹的 handler 仍能通过
// w.(http.Flusher) 类型断言拿到 Flusher(SSE 流式响应等场景必需)。
// 不透传会导致依赖 Flusher 的 handler 退化报错。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
