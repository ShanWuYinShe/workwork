package auth

import (
	"admin/internal/requestctx"
	"log"
	"net/http"
)

// AuditMiddleware 为路由表里 Audit=true 的端点补一条统一的成功审计行。
// 各 handler 仍保留带业务细节的 [security] 行；这一层保证“路由声明为变更
// 类端点”与“实际留下审计痕迹”不会脱节——此前 routes.go 的 Audit 字段只被
// 测试读取，漏写日志没有任何门禁能发现。
//
// 只在 status < 400 时记录；失败请求由错误日志/请求日志承担，避免把失败
// 伪装成已执行。pattern 使用路由模板（{uuid} 等），不带用户可控的实际值。
func AuditMiddleware(pattern string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		if rec.status >= 400 {
			return
		}
		actor, _ := requestctx.ActorFrom(r.Context())
		log.Printf("[security] audit endpoint=%s method=%s by=%s", pattern, r.Method, actor.Username)
	}
}
