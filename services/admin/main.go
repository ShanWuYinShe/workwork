package main

import (
	"admin/internal/auth"
	"admin/internal/downloads"
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
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/client"
)

// version 由构建期注入(-ldflags -X main.version,值为 workwork 仓库
// git describe),经 /api/admin/health 与启动日志暴露,未注入时为 "dev"。
var version = "dev"

func main() {
	port := platform.AdminPort()

	layout := runtime.DefaultLayout()
	// 必须在构造任何 store/启动任何 goroutine 之前清理：rate limiter 与
	// blocklist 同步会在运行期创建 .tmp-*，边清理边写盘可能删掉正在写的
	// 临时文件，使 rename 失败、写入丢失。
	platform.CleanupStaleTempFiles(layout.DataDir)
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
	downloadsMgr := downloads.NewManager(layout)
	// 清理上次运行中断下载残留（不阻断启动，失败只影响磁盘占用）。
	downloadsMgr.PurgeStaleDownloadTemps()
	trafficTracker := proxy.NewTrafficTracker(layout)
	ipv6State := proxy.NewIPv6State(layout)

	// Auto-generate configs from templates before dependent services start.
	if err := generateConfigs(users, secrets, layout, xraySvc, securitySvc); err != nil {
		log.Fatalf("Config generation failed: %v", err)
	}

	// 台账规范化可能把客户端 email 从登录用户名改写为客户端 UUID；把
	// 旧键→新键映射交给流量统计，续接历史累计值。
	trafficTracker.SetEmailAliases(xraySvc.EmailAliases())
	// 封禁同步到 nginx 层需要 docker exec reload;注入 client 并在启动时
	// 同步一次(把上次运行的封禁状态恢复到 nginx,nginx 容器重建后同样生效)。
	securitySvc.SetBlocklistClient(cli)
	securitySvc.ScheduleBlocklistSync()
	// 删用户级联清理其 transfer 上传通过 apiDeps 显式注入，保持 accounts
	// 不 import transfer，也避免"未接线时静默跳过"的可变全局。

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
	// 列表 + len() 注册：新增/删除守护不再需要手改 Add 数字（少加会 panic，
	// 多加会让关闭时的 Wait 永久等到超时）。
	daemons := []struct {
		name string
		run  func()
	}{
		{"xhttp-health", func() { proxy.XhttpHealthChecker(ctx, cli) }},
		{"ip-watcher", func() { proxy.IPWatcher(ctx, cli) }},
		{"ip-consistency", func() { proxy.IPConsistencyChecker(ctx, cli, startupRotateDone, ipv6State) }},
		// 证书到期监控(CF Origin CA 到期 = 全站 5xx,提前 30 天 webhook 告警)。
		{"cert-expiry", func() { infra.StartCertExpiryWatcher(ctx) }},
		// per-user 流量统计(xray StatsService 经 docker exec 拉取,每分钟)。
		{"traffic-collector", func() { trafficTracker.Start(ctx, cli) }},
		{"transfer-cleanup", func() { transferStore.StartCleanupLoop(ctx) }},
	}
	daemonWG.Add(len(daemons) + 1) // +1 = startup IPv6 restore goroutine
	for _, d := range daemons {
		go daemonGuard(&daemonWG, d.name, d.run)
	}

	mux := http.NewServeMux()
	mw := auth.NewMiddleware(users, secrets, securitySvc)

	// 路由表是唯一事实来源：角色、body-limit 豁免、审计属性都在 routes.go。
	routes := apiRoutes(apiDeps{
		cli:            cli,
		users:          users,
		xraySvc:        xraySvc,
		transfer:       transferStore,
		security:       securitySvc,
		downloads:      downloadsMgr,
		traffic:        trafficTracker,
		ipv6State:      ipv6State,
		cascadeUploads: transferStore.DeleteUploadsByUploader,
		layout:         layout,
	})
	bodyLimits := platform.NewBodyLimits(bodyLimitExemptions(routes))
	registerRoutes(mux, routes, mw)

	registerStaticRoutes(mux)
	handler := newHTTPHandler(mux, mw, bodyLimits)

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
	// 的 .tmp-* 等临时文件已在启动最前面统一回收(此时仍可能有下载/上传的
	// 专项清理，它们各自处理自己的前缀)。
	transferStore.CleanupOrphanTempFiles()

	go func() {
		log.Printf("admin %s listening on %s", version, srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Restore DNS-published rotating IPv6 addresses after containers are up.
	go func() {
		defer daemonWG.Done()
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
		proxy.StartupRotateIPv6(ctx, cli, ipv6State)
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
