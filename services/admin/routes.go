package main

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/downloads"
	"admin/internal/identity"
	"admin/internal/infra"
	"admin/internal/platform"
	"admin/internal/proxy"
	"admin/internal/runtime"
	"admin/internal/transfer"
	"admin/internal/xray"
	"log"
	"net/http"

	"github.com/docker/docker/client"
)

// 角色取值。空串表示公开端点（不套 RequireRole）；其它值必须是 RequireRole
// 认识的角色，契约测试会锁死取值集合，防止拼写错误悄悄把端点变成公开。
const (
	rolePublic = ""
	roleViewer = "viewer"
	roleAdmin  = "admin"
)

// apiRoute 是管理 API 的唯一事实来源：注册、RBAC、body-limit 豁免、审计
// 属性都挂在同一条记录上。此前这些信息分散在 main() 的 47 处内联
// HandleFunc + init() 的豁免清单 + 各 handler 的日志里，无法被测试枚举。
type apiRoute struct {
	Method  string
	Pattern string
	Handler http.HandlerFunc
	// Role 为空表示公开；否则为 viewer/admin。
	Role string
	// BodyLimitExempt 表示该端点自行限制请求体（大上传）。
	BodyLimitExempt bool
	// Audit 表示成功路径必须记 [security] 审计行（变更类端点）。
	Audit bool
}

// PatternWithMethod 返回 http.ServeMux 需要的 "METHOD /path" 形式。
func (r apiRoute) PatternWithMethod() string {
	return r.Method + " " + r.Pattern
}

// apiRoutes 构造全部管理 API 路由。cli 允许为 nil：契约测试只读元数据，
// 不会调用 handler。SPA/静态资源不属于 API 表，仍在 main() 注册。
func apiRoutes(cli *client.Client, users *identity.Store, xraySvc *xray.Service, transferStore *transfer.Store, layout runtime.Layout) []apiRoute {
	return []apiRoute{
		// ── 认证 / 账户 ──
		{Method: http.MethodGet, Pattern: "/api/admin/auth/me", Handler: accounts.ApiWhoAmI(), Role: roleViewer},
		{Method: http.MethodGet, Pattern: "/api/admin/users", Handler: accounts.ApiUsers(users, xraySvc), Role: roleViewer},
		{Method: http.MethodPost, Pattern: "/api/admin/users/password", Handler: accounts.ApiChangePassword(users), Role: roleViewer, Audit: true},
		{Method: http.MethodPost, Pattern: "/api/admin/users/admin/add", Handler: accounts.ApiAddAdmin(users), Role: roleAdmin, Audit: true},
		{Method: http.MethodPost, Pattern: "/api/admin/users/admin/delete", Handler: accounts.ApiDeleteAdmin(users, xraySvc), Role: roleAdmin, Audit: true},
		{Method: http.MethodPost, Pattern: "/api/admin/users/roles/set", Handler: accounts.ApiSetRole(users), Role: roleAdmin, Audit: true},

		// ── Xray 客户端 ──
		{Method: http.MethodPost, Pattern: "/api/admin/users/xray/add", Handler: accounts.ApiAddXray(users, xraySvc), Role: roleViewer, Audit: true},
		{Method: http.MethodPost, Pattern: "/api/admin/users/xray/delete", Handler: accounts.ApiDeleteXray(xraySvc), Role: roleViewer, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/users/xray/config/{uuid}", Handler: accounts.ApiXrayClientConfig(users, xraySvc), Role: roleViewer},

		// ── 面板 / 服务 / 日志 ──
		{Method: http.MethodGet, Pattern: "/api/admin/dashboard", Handler: infra.ApiDashboard(users, xraySvc, cli), Role: roleViewer},
		{Method: http.MethodGet, Pattern: "/api/admin/services", Handler: infra.ApiServices(cli), Role: roleAdmin},
		{Method: http.MethodPost, Pattern: "/api/admin/services/{name}/{action}", Handler: infra.ApiServiceAction(cli), Role: roleAdmin, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/services/{name}/logs", Handler: infra.ApiServiceLogs(cli), Role: roleAdmin},
		{Method: http.MethodGet, Pattern: "/api/admin/services/{name}/logs/stream", Handler: infra.ApiServiceLogStream(cli), Role: roleAdmin},
		{Method: http.MethodGet, Pattern: "/api/admin/config", Handler: infra.ApiConfig(layout), Role: roleViewer},

		// ── 网络 / 安全 / 流量 ──
		{Method: http.MethodGet, Pattern: "/api/admin/network/status", Handler: proxy.ApiNetworkStatus(cli), Role: roleAdmin},
		{Method: http.MethodPost, Pattern: "/api/admin/network/rotate-ipv6", Handler: proxy.ApiRotateIPv6(cli), Role: roleAdmin, Audit: true},
		{Method: http.MethodPost, Pattern: "/api/admin/network/sync-dns", Handler: proxy.ApiSyncDNS(cli), Role: roleAdmin, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/security/blocked", Handler: auth.ApiBlockedIPs(), Role: roleAdmin},
		{Method: http.MethodPost, Pattern: "/api/admin/security/block", Handler: auth.ApiBlockIP(), Role: roleAdmin, Audit: true},
		{Method: http.MethodPost, Pattern: "/api/admin/security/unblock", Handler: auth.ApiUnblockIP(), Role: roleAdmin, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/security/rate-limit", Handler: auth.ApiRateLimitStatus(), Role: roleAdmin},
		{Method: http.MethodPost, Pattern: "/api/admin/security/rate-limit/unblock", Handler: auth.ApiRateLimitUnblock(), Role: roleAdmin, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/traffic", Handler: proxy.ApiTraffic(), Role: roleAdmin},

		// ── 公开端点 ──
		{Method: http.MethodGet, Pattern: "/api/admin/health", Handler: healthHandler(layout), Role: rolePublic},
		{Method: http.MethodGet, Pattern: "/speedtest/{size}", Handler: infra.ApiSpeedtest(), Role: rolePublic},
		{Method: http.MethodGet, Pattern: "/api/transfer/{code}", Handler: transfer.ApiTransferPublicDownload(transferStore), Role: rolePublic},

		// ── Claude Code 下载 ──
		{Method: http.MethodGet, Pattern: "/api/admin/claude-code/releases", Handler: downloads.ApiClaudeCodeReleases(), Role: roleAdmin},
		{Method: http.MethodPost, Pattern: "/api/admin/claude-code/refresh", Handler: downloads.ApiClaudeCodeRefresh(), Role: roleAdmin, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/claude-code/platforms/{version}", Handler: downloads.ApiClaudeCodePlatforms(), Role: roleAdmin},
		{Method: http.MethodGet, Pattern: "/api/admin/claude-code/stored", Handler: downloads.ApiClaudeCodeStored(), Role: roleViewer},
		{Method: http.MethodPost, Pattern: "/api/admin/claude-code/download", Handler: downloads.ApiClaudeCodeDownload(), Role: roleAdmin, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/claude-code/downloads/{filename}", Handler: downloads.ApiClaudeCodeServeDownload(), Role: roleViewer},
		{Method: http.MethodDelete, Pattern: "/api/admin/claude-code/downloads", Handler: downloads.ApiClaudeCodeDeleteDownload(), Role: roleAdmin, Audit: true},

		// ── Xray 下载 ──
		{Method: http.MethodGet, Pattern: "/api/admin/xray/releases", Handler: downloads.ApiXrayReleases(), Role: roleAdmin},
		{Method: http.MethodPost, Pattern: "/api/admin/xray/refresh", Handler: downloads.ApiXrayRefresh(), Role: roleAdmin, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/xray/platforms/{version}", Handler: downloads.ApiXrayPlatforms(), Role: roleAdmin},
		{Method: http.MethodGet, Pattern: "/api/admin/xray/stored", Handler: downloads.ApiXrayStored(), Role: roleViewer},
		{Method: http.MethodPost, Pattern: "/api/admin/xray/download", Handler: downloads.ApiXrayDownload(), Role: roleAdmin, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/xray/downloads/{filename}", Handler: downloads.ApiXrayServeDownload(), Role: roleViewer},
		{Method: http.MethodDelete, Pattern: "/api/admin/xray/downloads", Handler: downloads.ApiXrayDeleteDownload(), Role: roleAdmin, Audit: true},

		// ── 文件中转 ──
		{Method: http.MethodPost, Pattern: "/api/admin/transfer/upload", Handler: transfer.ApiTransferUpload(transferStore), Role: roleViewer, BodyLimitExempt: true, Audit: true},
		{Method: http.MethodGet, Pattern: "/api/admin/transfer/files", Handler: transfer.ApiTransferList(transferStore), Role: roleViewer},
		{Method: http.MethodDelete, Pattern: "/api/admin/transfer/files", Handler: transfer.ApiTransferDelete(transferStore), Role: roleViewer, Audit: true},
	}
}

// healthHandler 是公开的健康检查（admin 层无业务认证；经 nginx 时仍要
// basic auth，机器探针应使用 /healthz）。
func healthHandler(layout runtime.Layout) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := runtimeFilesReady(layout); err != nil {
			log.Printf("health check failed: %v", err)
			platform.WriteError(w, "服务初始化中，请稍后重试", http.StatusServiceUnavailable)
			return
		}
		platform.WriteJSON(w, map[string]string{"status": "ok", "version": version})
	}
}

// registerRoutes 统一套 RBAC 并注册；角色拼写错误在启动时就会被
// RequireRole 的默认分支拒绝（契约测试也会枚举）。
func registerRoutes(mux *http.ServeMux, routes []apiRoute, mw *auth.Middleware) {
	for _, rt := range routes {
		h := rt.Handler
		if rt.Role != rolePublic {
			h = mw.RequireRole(rt.Role)(h)
		}
		mux.HandleFunc(rt.PatternWithMethod(), h)
	}
}

// registerBodyLimitExemptions 把路由表里的大上传声明同步到 platform。
// 唯一事实源是 apiRoute.BodyLimitExempt，不再有 init() 的散落清单。
func registerBodyLimitExemptions(routes []apiRoute) {
	for _, rt := range routes {
		if rt.BodyLimitExempt {
			platform.ExemptBodyLimit(rt.Pattern)
		}
	}
}
