package main

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/downloads"
	"admin/internal/infra"
	"admin/internal/media"
	"admin/internal/music"
	"admin/internal/platform"
	"admin/internal/proxy"
	"admin/internal/transfer"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/client"
)

func main() {
	port := platform.GetEnv("ADMIN_PORT")
	if port == "" {
		port = "8318"
	}

	// Auto-generate configs from templates before dependent services start.
	if err := generateConfigs(); err != nil {
		log.Fatalf("Config generation failed: %v", err)
	}

	// 不要用 client.WithTimeout:它设置的是 http.Client.Timeout,即整个
	// 请求-响应周期(含 body 读取)的硬墙,会把无 deadline 的长连接流掐断——
	// 服务日志 SSE(/api/admin/services 的 logs/stream,Follow:true)会在
	// 60s 后被硬切,proxy.IPWatcher 的 Events 流也会每 60s 断开重连。
	// 需要超时兜底的自愈调用改为在调用点显式包 context deadline(见
	// proxy.XhttpHealthChecker 的 proxy.RestartXray、proxy.IPWatcher 的 proxy.RestoreServiceRotatingIP)。
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	// 封禁同步到 nginx 层需要 docker exec reload;注入 client 并在启动时
	// 同步一次(把上次运行的封禁状态恢复到 nginx,nginx 容器重建后同样生效)。
	auth.SetBlocklistClient(cli)
	auth.ScheduleBlocklistSync()

	// 启动 IPv6 恢复完成后关闭此 channel，proxy.IPConsistencyChecker 等待它避免竞态。
	startupRotateDone := make(chan struct{})

	go proxy.XhttpHealthChecker(context.Background(), cli)
	go proxy.IPWatcher(context.Background(), cli)
	go proxy.IPConsistencyChecker(context.Background(), cli, startupRotateDone)

	mux := http.NewServeMux()

	// Auth — available to all authenticated users
	mux.HandleFunc("GET /api/admin/auth/me", auth.RequireRole("viewer")(accounts.ApiWhoAmI(cli)))

	// Dashboard — viewer+ (returns role-appropriate data)
	mux.HandleFunc("GET /api/admin/dashboard", auth.RequireRole("viewer")(infra.ApiDashboard(cli)))
	mux.HandleFunc("GET /api/admin/services", auth.RequireRole("admin")(infra.ApiServices(cli)))
	// 兜底：nginx 历史上曾把 /api/admin/services 301 到带斜杠路径，直接返回列表
	// 而不是 404，避免浏览器跟随重定向后页面报错。
	mux.HandleFunc("GET /api/admin/services/", auth.RequireRole("admin")(infra.ApiServices(cli)))
	mux.HandleFunc("GET /api/admin/config", auth.RequireRole("viewer")(infra.ApiConfig(cli)))

	// User operations — viewer+ (read own), admin (manage all)
	mux.HandleFunc("GET /api/admin/users", auth.RequireRole("viewer")(accounts.ApiUsers(cli)))
	mux.HandleFunc("POST /api/admin/users/password", auth.RequireRole("viewer")(accounts.ApiChangePassword(cli)))

	// Admin-only operations
	mux.HandleFunc("POST /api/admin/services/{name}/{action}", auth.RequireRole("admin")(infra.ApiServiceAction(cli)))
	// Xray client management — viewers can manage their own clients
	mux.HandleFunc("POST /api/admin/users/xray/add", auth.RequireRole("viewer")(accounts.ApiAddXray(cli)))
	mux.HandleFunc("POST /api/admin/users/xray/delete", auth.RequireRole("viewer")(accounts.ApiDeleteXray(cli)))
	mux.HandleFunc("GET /api/admin/users/xray/config/{uuid}", auth.RequireRole("viewer")(accounts.ApiXrayClientConfig()))
	mux.HandleFunc("POST /api/admin/users/admin/add", auth.RequireRole("admin")(accounts.ApiAddAdmin(cli)))
	mux.HandleFunc("POST /api/admin/users/admin/delete", auth.RequireRole("admin")(accounts.ApiDeleteAdmin(cli)))
	mux.HandleFunc("POST /api/admin/users/roles/set", auth.RequireRole("admin")(accounts.ApiSetRole(cli)))
	mux.HandleFunc("GET /api/admin/network/status", auth.RequireRole("admin")(proxy.ApiNetworkStatus(cli)))
	mux.HandleFunc("POST /api/admin/network/rotate-ipv6", auth.RequireRole("admin")(proxy.ApiRotateIPv6(cli)))
	mux.HandleFunc("POST /api/admin/network/sync-dns", auth.RequireRole("admin")(proxy.ApiSyncDNS(cli)))
	mux.HandleFunc("GET /api/admin/media-center/config", auth.RequireRole("admin")(media.ApiMediaCenterConfig()))
	mux.HandleFunc("PUT /api/admin/media-center/config", auth.RequireRole("admin")(media.ApiSaveMediaCenterConfig(cli)))
	mux.HandleFunc("GET /api/admin/media-center/credentials", auth.RequireRole("admin")(media.ApiMediaCenterCredentials()))
	mux.HandleFunc("PUT /api/admin/media-center/credentials", auth.RequireRole("admin")(media.ApiSaveMediaCenterCredentials(cli)))
	// 分类审核管理（catalog 人工审核：导入候选 → 审核 → 上线 approved）。
	mux.HandleFunc("GET /api/admin/media-center/catalog/overview", auth.RequireRole("admin")(media.ApiMediaCatalogOverview()))
	mux.HandleFunc("GET /api/admin/media-center/catalog/items", auth.RequireRole("admin")(media.ApiMediaCatalogItems()))
	mux.HandleFunc("GET /api/admin/media-center/catalog/export", auth.RequireRole("admin")(media.ApiMediaCatalogExport()))
	mux.HandleFunc("POST /api/admin/media-center/catalog/review", auth.RequireRole("admin")(media.ApiMediaCatalogReview()))
	mux.HandleFunc("POST /api/admin/media-center/catalog/delete", auth.RequireRole("admin")(media.ApiMediaCatalogDelete()))
	mux.HandleFunc("POST /api/admin/media-center/catalog/import", auth.RequireRole("admin")(media.ApiMediaCatalogImport()))
	mux.HandleFunc("GET /api/admin/media-center/app-release", auth.RequireRole("admin")(media.ApiMediaAppReleaseGet()))
	mux.HandleFunc("POST /api/admin/media-center/app-release", auth.RequireRole("admin")(media.ApiMediaAppReleaseUpload()))
	// 版本管理：发布历史 / 回滚 / 删除历史版本 / 管理员下载指定版本 APK。
	mux.HandleFunc("GET /api/admin/media-center/app-release/history", auth.RequireRole("admin")(media.ApiMediaAppReleaseHistory()))
	mux.HandleFunc("GET /api/admin/media-center/app-release/download", auth.RequireRole("admin")(media.ApiMediaAppReleaseDownload()))
	mux.HandleFunc("POST /api/admin/media-center/app-release/rollback", auth.RequireRole("admin")(media.ApiMediaAppReleaseRollback()))
	mux.HandleFunc("DELETE /api/admin/media-center/app-release/history", auth.RequireRole("admin")(media.ApiMediaAppReleaseVersionDelete()))
	// 音乐曲库：列表 / 上传 / 删除（写入 music_server 扫描目录，上传即入库）
	mux.HandleFunc("GET /api/admin/music-library", auth.RequireRole("admin")(music.ApiMusicLibraryList()))
	mux.HandleFunc("POST /api/admin/music-library/upload", auth.RequireRole("admin")(music.ApiMusicLibraryUpload()))
	// 分片上传（大文件绕过 CF 单请求 100MB 上限 + 断点续传）
	mux.HandleFunc("POST /api/admin/music-library/upload-chunk", auth.RequireRole("admin")(music.ApiMusicChunkUpload()))
	mux.HandleFunc("GET /api/admin/music-library/upload-chunk", auth.RequireRole("admin")(music.ApiMusicChunkQuery()))
	mux.HandleFunc("DELETE /api/admin/music-library", auth.RequireRole("admin")(music.ApiMusicLibraryDelete()))
	// 网易云账号扫码登录：二维码生成 / 轮询（成功写 cookie 文件）/ 状态 / 登出
	mux.HandleFunc("POST /api/admin/music/netease/login/start", auth.RequireRole("admin")(music.ApiNeteaseLoginStart()))
	mux.HandleFunc("GET /api/admin/music/netease/login/poll", auth.RequireRole("admin")(music.ApiNeteaseLoginPoll()))
	mux.HandleFunc("GET /api/admin/music/netease/login/state", auth.RequireRole("admin")(music.ApiNeteaseLoginState()))
	mux.HandleFunc("POST /api/admin/music/netease/login/logout", auth.RequireRole("admin")(music.ApiNeteaseLoginLogout()))
	// IP block management (Go-level, works behind Cloudflare)
	mux.HandleFunc("GET /api/admin/security/blocked", auth.RequireRole("admin")(auth.ApiBlockedIPs()))
	mux.HandleFunc("POST /api/admin/security/block", auth.RequireRole("admin")(auth.ApiBlockIP()))
	mux.HandleFunc("POST /api/admin/security/unblock", auth.RequireRole("admin")(auth.ApiUnblockIP()))

	// Health check (no auth required)
	mux.HandleFunc("GET /api/admin/health", func(w http.ResponseWriter, _ *http.Request) {
		if err := runtimeFilesReady(); err != nil {
			log.Printf("health check failed: %v", err)
			platform.WriteError(w, "Not ready", http.StatusServiceUnavailable)
			return
		}
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	})

	// Claude Code downloads — admin manages, viewer can see + download stored
	mux.HandleFunc("GET /api/admin/claude-code/releases", auth.RequireRole("admin")(downloads.ApiClaudeCodeReleases()))
	mux.HandleFunc("POST /api/admin/claude-code/refresh", auth.RequireRole("admin")(downloads.ApiClaudeCodeRefresh()))
	mux.HandleFunc("GET /api/admin/claude-code/platforms/{version}", auth.RequireRole("admin")(downloads.ApiClaudeCodePlatforms()))
	mux.HandleFunc("GET /api/admin/claude-code/stored", auth.RequireRole("viewer")(downloads.ApiClaudeCodeStored()))
	mux.HandleFunc("POST /api/admin/claude-code/download", auth.RequireRole("admin")(downloads.ApiClaudeCodeDownload()))
	mux.HandleFunc("GET /api/admin/claude-code/downloads/{filename}", auth.RequireRole("viewer")(downloads.ApiClaudeCodeServeDownload()))
	mux.HandleFunc("DELETE /api/admin/claude-code/downloads", auth.RequireRole("admin")(downloads.ApiClaudeCodeDeleteDownload()))
	mux.HandleFunc("DELETE /api/admin/claude-code/downloads/", auth.RequireRole("admin")(downloads.ApiClaudeCodeDeleteDownload()))
	// Service logs
	mux.HandleFunc("GET /api/admin/services/{name}/logs", auth.RequireRole("admin")(infra.ApiServiceLogs(cli)))
	mux.HandleFunc("GET /api/admin/services/{name}/logs/stream", auth.RequireRole("admin")(infra.ApiServiceLogStream(cli)))
	// Xray downloads — admin manages, viewer can see + download stored
	mux.HandleFunc("GET /api/admin/xray/releases", auth.RequireRole("admin")(downloads.ApiXrayReleases()))
	mux.HandleFunc("POST /api/admin/xray/refresh", auth.RequireRole("admin")(downloads.ApiXrayRefresh()))
	mux.HandleFunc("GET /api/admin/xray/platforms/{version}", auth.RequireRole("admin")(downloads.ApiXrayPlatforms()))
	mux.HandleFunc("GET /api/admin/xray/stored", auth.RequireRole("viewer")(downloads.ApiXrayStored()))
	mux.HandleFunc("POST /api/admin/xray/download", auth.RequireRole("admin")(downloads.ApiXrayDownload()))
	mux.HandleFunc("GET /api/admin/xray/downloads/{filename}", auth.RequireRole("viewer")(downloads.ApiXrayServeDownload()))
	mux.HandleFunc("DELETE /api/admin/xray/downloads", auth.RequireRole("admin")(downloads.ApiXrayDeleteDownload()))
	mux.HandleFunc("DELETE /api/admin/xray/downloads/", auth.RequireRole("admin")(downloads.ApiXrayDeleteDownload()))
	// Rate limit management (admin only)
	mux.HandleFunc("GET /api/admin/security/rate-limit", auth.RequireRole("admin")(auth.ApiRateLimitStatus()))
	mux.HandleFunc("POST /api/admin/security/rate-limit/unblock", auth.RequireRole("admin")(auth.ApiRateLimitUnblock()))
	// File transfer station — viewer+ uploads/lists, public download via share code
	mux.HandleFunc("POST /api/admin/transfer/upload", auth.RequireRole("viewer")(transfer.ApiTransferUpload()))
	mux.HandleFunc("GET /api/admin/transfer/files", auth.RequireRole("viewer")(transfer.ApiTransferList()))
	mux.HandleFunc("DELETE /api/admin/transfer/files", auth.RequireRole("viewer")(transfer.ApiTransferDelete()))
	// Public transfer download — no auth required
	mux.HandleFunc("GET /api/transfer/{code}", transfer.ApiTransferPublicDownload())

	// Svelte SPA - serve at /admin/ (any subpath serves index.html)
	mux.HandleFunc("GET /admin/", serveSPA)
	mux.HandleFunc("GET /admin", serveSPA)

	// Svelte static assets - serve at absolute paths
	static := http.FileServer(http.Dir("/app/ui"))
	mux.Handle("/assets/", static)
	mux.Handle("/favicon.svg", static)
	mux.Handle("/icons.svg", static)

	// Wrap all routes: security headers -> IP block -> rate limit -> serve
	// Static assets bypass rate limiting to avoid consuming request quota.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// Transfer upload streams large bodies (per-file cap enforced in the
		// handler via io.LimitReader); use exact match to avoid false positives
		// on similar paths.
		platform.ApplyBodyLimit(w, r)
		// Static assets: /assets/ 是指纹文件名(内容哈希),可永久缓存;
		// favicon.svg / icons.svg 非指纹,更新后需尽快生效,只给短缓存。
		// 两者都跳过 rate limit(高频低价值)。
		if platform.IsStaticAsset(r.URL.Path) {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
			auth.IPBlockMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
				mux.ServeHTTP(w2, r2)
			})(w, r)
			return
		}

		auth.RateLimitMiddleware(auth.IPBlockMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
			mux.ServeHTTP(w2, r2)
		}))(w, r)
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: auth.RequestLogMiddleware(handler),
		// 大文件上传（transfer 2GB / APK 300MB）需要长时间读取请求体，30s 单一
		// 读截止时间会让慢速上传被服务端掐断；读超时交由 nginx 层管理，
		// ReadHeaderTimeout 仍防止慢连接头攻击，请求体大小由 MaxBytesReader 限制。
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	auth.StartRateLimiter()

	// Graceful shutdown — context 创建在 cleanup goroutine 之前，
	// 这样 transfer.StartCleanupLoop 能响应 SIGTERM 信号优雅退出。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 优雅关闭时停止 rate limiter 的 cleanup goroutine，避免 goroutine 泄漏
	// 到进程退出之外（也顺带让最后一次 ipBlocker.save() 有机会执行）。
	defer auth.StopRateLimiter()

	transfer.CleanupOrphanTempFiles()
	go transfer.StartCleanupLoop(ctx)
	music.CleanupExpiredChunkUploads()
	go music.StartChunkCleanupLoop(ctx)

	go func() {
		log.Printf("Admin panel listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Restore DNS-published rotating IPv6 addresses after containers are up.
	go func() {
		defer close(startupRotateDone)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[startup] panic in proxy.StartupRotateIPv6: %v", r)
			}
		}()
		time.Sleep(10 * time.Second)
		proxy.StartupRotateIPv6(cli)
	}()

	<-ctx.Done()
	log.Println("Shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("Server stopped")
}

// 大上传端点豁免全局 1MB 请求体上限。与下方路由注册一一对应,新增大上传
// 端点时必须在此同步声明;main_test.go 的契约测试锁住该清单。
func init() {
	platform.ExemptBodyLimit("/api/admin/transfer/upload")             // handler: 2GB
	platform.ExemptBodyLimit("/api/admin/media-center/app-release")    // APK 发布上传
	platform.ExemptBodyLimit("/api/admin/media-center/catalog/import") // 目录导入
	platform.ExemptBodyLimit("/api/admin/music-library/upload")        // handler: 408MB(multipart)
	platform.ExemptBodyLimit("/api/admin/music-library/upload-chunk")  // handler: 单片 8MB+开销
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/app/ui/index.html")
}
