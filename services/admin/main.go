package main

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/identity"
	"admin/internal/infra"
	"admin/internal/platform"
	"admin/internal/proxy"
	"admin/internal/runtime"
	"admin/internal/transfer"
	"admin/internal/xray"
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

	layout := runtime.DefaultLayout()
	notifier := runtime.NotifyFunc(platform.NotifyWebhook)
	users := identity.NewStore(layout, notifier)
	secrets := identity.NewSecrets(layout)
	securitySvc := auth.NewSecurity(layout)

	// 不要用 client.WithTimeout:它设置的是 http.Client.Timeout,即整个
	// 请求-响应周期(含 body 读取)的硬墙,会把无 deadline 的长连接流掐断——
	// 服务日志 SSE(/api/admin/services 的 logs/stream,Follow:true)会在
	// 60s 后被硬切,proxy.IPWatcher 的 Events 流也会每 60s 断开重连。
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	defer cli.Close()

	xraySvc := xray.NewService(layout, notifier, proxy.NewXrayRestarter(cli))
	transferStore := transfer.NewStore(layout)

	// Auto-generate configs from templates before dependent services start.
	if err := generateConfigs(users, secrets, layout, xraySvc, securitySvc); err != nil {
		log.Fatalf("Config generation failed: %v", err)
	}

	// 台账规范化可能把客户端 email 从登录用户名改写为客户端 UUID；把
	// 旧键→新键映射交给流量统计，续接历史累计值。
	proxy.SetTrafficEmailAliases(xraySvc.EmailAliases())
	// 封禁同步到 nginx 层需要 docker exec reload;注入 client 并在启动时
	// 同步一次(把上次运行的封禁状态恢复到 nginx,nginx 容器重建后同样生效)。
	securitySvc.SetBlocklistClient(cli)
	securitySvc.ScheduleBlocklistSync()
	// 删用户级联清理其 transfer 上传(钩子注入,保持 accounts 不依赖 transfer)。
	accounts.CascadeDeleteUploads = transferStore.DeleteUploadsByUploader

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
	daemonWG.Add(5)
	go daemonGuard(&daemonWG, "xhttp-health", func() { proxy.XhttpHealthChecker(ctx, cli) })
	go daemonGuard(&daemonWG, "ip-watcher", func() { proxy.IPWatcher(ctx, cli) })
	go daemonGuard(&daemonWG, "ip-consistency", func() { proxy.IPConsistencyChecker(ctx, cli, startupRotateDone) })
	// 证书到期监控(CF Origin CA 到期 = 全站 5xx,提前 30 天 webhook 告警)。
	go daemonGuard(&daemonWG, "cert-expiry", func() { infra.StartCertExpiryWatcher(ctx) })
	// per-user 流量统计(xray StatsService 经 docker exec 拉取,每分钟)。
	go daemonGuard(&daemonWG, "traffic-collector", func() { proxy.StartTrafficCollector(ctx, cli) })

	mux := http.NewServeMux()
	mw := auth.NewMiddleware(users, secrets, securitySvc)

	// 路由表是唯一事实来源：角色、body-limit 豁免、审计属性都在 routes.go。
	routes := apiRoutes(cli, users, xraySvc, transferStore, securitySvc, layout)
	registerBodyLimitExemptions(routes)
	registerRoutes(mux, routes, mw)

	// Svelte SPA - serve at /admin/ (any subpath serves index.html)
	mux.HandleFunc("GET /admin/", serveSPA)
	mux.HandleFunc("GET /admin", serveSPA)

	// Svelte static assets - serve at absolute paths
	static := http.FileServer(http.Dir("/app/ui"))
	mux.Handle("/assets/", static)
	mux.Handle("/favicon.svg", static)
	mux.Handle("/icons.svg", static)

	// Wrap all routes: CSRF -> IP block -> rate limit -> serve
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
			mw.IPBlockMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
				mux.ServeHTTP(w2, r2)
			})(w, r)
			return
		}

		// API 与 SPA 响应禁止缓存:no-store 同时关闭磁盘缓存与 bfcache,
		// 防止登出后浏览器回退页残留管理数据(users 列表内嵌明文代理凭证)。
		w.Header().Set("Cache-Control", "no-store")
		// CSRF 校验必须在限流之前:跨站伪造请求来自受害者自己的浏览器与 IP,
		// 若先限流,攻击会耗尽受害者 IP 的配额触发 30min 自动封禁(限流器被
		// 武器化为 DoS 受害者)。IP 封禁必须在限流之前:已封禁 IP 不应再消耗
		// 限流预算、触发封禁写盘/webhook（否则封禁后仍可周期性放大写盘）。
		// 安全头/静态资源分支不需要这些（均为 GET）。
		auth.CSRFMiddleware(mw.IPBlockMiddleware(mw.RateLimitMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
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

	securitySvc.StartRateLimiter()

	// Graceful shutdown — 信号 ctx 已在守护 goroutine 启动前创建（见上方
	// proxy 三守护），transfer 的 cleanup loop 与它们共享同一 ctx。
	// 优雅关闭时停止 rate limiter 的 cleanup goroutine，避免 goroutine 泄漏
	// 到进程退出之外（也顺带让最后一次 ipBlocker.save() 有机会执行）。
	defer securitySvc.StopRateLimiter()

	// SIGKILL(磁盘满 OOM/宿主重启/部署 stop 超时)会让 defer 删除失效,残留
	// 的 .tmp-* 等临时文件在此统一兜底回收(覆盖各写入路径)。
	platform.CleanupStaleTempFiles(dataDir)

	transferStore.CleanupOrphanTempFiles()
	daemonWG.Add(1)
	go daemonGuard(&daemonWG, "transfer-cleanup", func() { transferStore.StartCleanupLoop(ctx) })

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
	// 去抖中的封禁写入必须在这里落盘，否则限流热路径最后 1s 的封禁会丢。
	securitySvc.FlushBlockedIPs()
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

func serveSPA(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/app/ui/index.html")
}
