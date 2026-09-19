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
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/client"
)

// version 由构建期注入(-ldflags -X main.version,值为 workwork 仓库
// git describe),经 /api/admin/health 与启动日志暴露,未注入时为 "dev"。
var version = "dev"

// dataDir 是运行时数据根(compose 把宿主 .wework 挂载于此)。var 供测试替换。
var dataDir = "/app/data"

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
	// XhttpHealthChecker 的 RestartXray、IPWatcher 的 RestoreServiceRotatingIP)。
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	// 优雅关闭路径释放 docker.sock 连接(进程退出也会兜底,显式 Close 让
	// 生命周期完整,测试内嵌运行时不留悬空连接)。
	defer cli.Close()
	// 封禁同步到 nginx 层需要 docker exec reload;注入 client 并在启动时
	// 同步一次(把上次运行的封禁状态恢复到 nginx,nginx 容器重建后同样生效)。
	auth.SetBlocklistClient(cli)
	auth.ScheduleBlocklistSync()
	// 删用户级联清理其 transfer 上传(钩子注入,保持 accounts 不依赖 transfer)。
	accounts.CascadeDeleteUploads = transfer.DeleteUploadsByUploader

	// 启动 IPv6 恢复完成后关闭此 channel，proxy.IPConsistencyChecker 等待它避免竞态。
	startupRotateDone := make(chan struct{})

	// 信号 ctx 必须先于守护 goroutine 创建：三个 proxy 守护（xhttp 自愈 /
	// IP watcher / IP 一致性检查）都监听 ctx.Done()，传 Background 会让它们
	// 在优雅关闭时无法退出（SSE/docker 流被进程消亡硬切）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 优雅关闭时等待守护 goroutine 退出:此前 main 返回即进程消亡,进行中的
	// RestoreServiceRotatingIP(容器 IP 补挂)可能被杀在半路,轮换 IP 丢失、
	// 代理出口失效直到下次启动兜底。带超时等待,守护卡死时不阻塞退出。
	var daemonWG sync.WaitGroup
	daemonWG.Add(3)
	go daemonGuard(&daemonWG, "xhttp-health", func() { proxy.XhttpHealthChecker(ctx, cli) })
	go daemonGuard(&daemonWG, "ip-watcher", func() { proxy.IPWatcher(ctx, cli) })
	go daemonGuard(&daemonWG, "ip-consistency", func() { proxy.IPConsistencyChecker(ctx, cli, startupRotateDone) })

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
		platform.WriteJSON(w, map[string]string{"status": "ok", "version": version})
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

		// API 与 SPA 响应禁止缓存:no-store 同时关闭磁盘缓存与 bfcache,
		// 防止登出后浏览器回退页残留管理数据(users 列表内嵌明文代理凭证)。
		w.Header().Set("Cache-Control", "no-store")
		// CSRF 校验必须在限流之前:跨站伪造请求来自受害者自己的浏览器与 IP,
		// 若先限流,攻击会耗尽受害者 IP 的配额触发 30min 自动封禁(限流器被
		// 武器化为 DoS 受害者)。安全头/静态资源分支不需要 CSRF(均为 GET)。
		auth.CSRFMiddleware(auth.RateLimitMiddleware(auth.IPBlockMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
			mux.ServeHTTP(w2, r2)
		})))(w, r)
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

	// Graceful shutdown — 信号 ctx 已在守护 goroutine 启动前创建（见上方
	// proxy 三守护），transfer/music 的 cleanup loop 与它们共享同一 ctx。
	// 优雅关闭时停止 rate limiter 的 cleanup goroutine，避免 goroutine 泄漏
	// 到进程退出之外（也顺带让最后一次 ipBlocker.save() 有机会执行）。
	defer auth.StopRateLimiter()

	// SIGKILL(磁盘满 OOM/宿主重启/部署 stop 超时)会让 defer 删除失效,残留
	// 的 .tmp-*/.music-*/.chunk-* 等临时文件在此统一兜底回收(覆盖各写入路径)。
	platform.CleanupStaleTempFiles(dataDir)

	transfer.CleanupOrphanTempFiles()
	daemonWG.Add(1)
	go daemonGuard(&daemonWG, "transfer-cleanup", func() { transfer.StartCleanupLoop(ctx) })
	music.CleanupExpiredChunkUploads()
	daemonWG.Add(1)
	go daemonGuard(&daemonWG, "chunk-cleanup", func() { music.StartChunkCleanupLoop(ctx) })

	go func() {
		log.Printf("admin %s listening on %s", version, srv.Addr)
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
		// ctx 感知等待:优雅关闭时不再挂满 10s 才退出。
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		proxy.StartupRotateIPv6(cli)
	}()

	<-ctx.Done()
	log.Println("Shutting down server...")
	// 先停监听再等守护:正常路径 srv.Shutdown 最多 10s(SSE/大上传会吃满,
	// compose stop_grace_period 前 docker 会 SIGKILL),守护随 ctx 退出后
	// WaitGroup 归零。最多再等 5s,守护卡死不阻塞进程退出。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	waitDone := make(chan struct{})
	go func() { daemonWG.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		log.Println("Shutdown: daemons did not exit in 5s, proceeding")
	}
	log.Println("Server stopped")
}

// daemonGuard 包装守护 goroutine:per-iteration recover 在各守护内部做
// (避免一次 panic 静默杀掉整个守护),这里只兜底守护启动期的 panic 并
// 追踪退出,供优雅关闭 WaitGroup 等待。
func daemonGuard(wg *sync.WaitGroup, name string, fn func()) {
	defer wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("daemon %s: startup panic: %v", name, r)
		}
	}()
	fn()
}

// 大上传端点豁免全局 1MB 请求体上限。与本文件路由注册一一对应,新增大
// 上传端点时必须在此同步声明;main_test.go 的契约测试锁住该清单。
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
