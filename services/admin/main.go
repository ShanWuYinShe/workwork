package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/docker/docker/client"
)

func main() {
	port := getEnv("ADMIN_PORT")
	if port == "" {
		port = "8318"
	}

	// Auto-generate configs from templates before dependent services start.
	if err := generateConfigs(); err != nil {
		log.Fatalf("Config generation failed: %v", err)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}

	// 启动 IPv6 恢复完成后关闭此 channel，ipConsistencyChecker 等待它避免竞态。
	startupRotateDone := make(chan struct{})

	go xhttpHealthChecker(context.Background(), cli)
	go ipWatcher(context.Background(), cli)
	go ipConsistencyChecker(context.Background(), cli, startupRotateDone)

	mux := http.NewServeMux()

	// Auth — available to all authenticated users
	mux.HandleFunc("GET /api/admin/auth/me", requireRole("viewer")(apiWhoAmI(cli)))

	// Dashboard — viewer+ (returns role-appropriate data)
	mux.HandleFunc("GET /api/admin/dashboard", requireRole("viewer")(apiDashboard(cli)))
	mux.HandleFunc("GET /api/admin/services", requireRole("admin")(apiServices(cli)))
	mux.HandleFunc("GET /api/admin/config", requireRole("viewer")(apiConfig(cli)))

	// User operations — viewer+ (read own), admin (manage all)
	mux.HandleFunc("GET /api/admin/users", requireRole("viewer")(apiUsers(cli)))
	mux.HandleFunc("POST /api/admin/users/password", requireRole("viewer")(apiChangePassword(cli)))

	// Admin-only operations
	mux.HandleFunc("POST /api/admin/services/{name}/{action}", requireRole("admin")(apiServiceAction(cli)))
	// Xray client management — viewers can manage their own clients
	mux.HandleFunc("POST /api/admin/users/xray/add", requireRole("viewer")(apiAddXray(cli)))
	mux.HandleFunc("POST /api/admin/users/xray/delete", requireRole("viewer")(apiDeleteXray(cli)))
	mux.HandleFunc("GET /api/admin/users/xray/config/{uuid}", requireRole("viewer")(apiXrayClientConfig()))
	mux.HandleFunc("POST /api/admin/users/admin/add", requireRole("admin")(apiAddAdmin(cli)))
	mux.HandleFunc("POST /api/admin/users/admin/delete", requireRole("admin")(apiDeleteAdmin(cli)))
	mux.HandleFunc("POST /api/admin/users/roles/set", requireRole("admin")(apiSetRole(cli)))
	mux.HandleFunc("GET /api/admin/network/status", requireRole("admin")(apiNetworkStatus(cli)))
	mux.HandleFunc("POST /api/admin/network/rotate-ipv6", requireRole("admin")(apiRotateIPv6(cli)))
	mux.HandleFunc("POST /api/admin/network/sync-dns", requireRole("admin")(apiSyncDNS(cli)))
	mux.HandleFunc("GET /api/admin/media-center/config", requireRole("admin")(apiMediaCenterConfig()))
	mux.HandleFunc("PUT /api/admin/media-center/config", requireRole("admin")(apiSaveMediaCenterConfig(cli)))
	// 分类预采集管理（catalog 审校 + 运行时快照 + 本地审校 curation.json）。
	mux.HandleFunc("GET /api/admin/media-center/catalog/overview", requireRole("admin")(apiMediaCatalogOverview()))
	mux.HandleFunc("GET /api/admin/media-center/catalog/items", requireRole("admin")(apiMediaCatalogItems()))
	mux.HandleFunc("GET /api/admin/media-center/catalog/export", requireRole("admin")(apiMediaCatalogExport()))
	mux.HandleFunc("POST /api/admin/media-center/catalog/review", requireRole("admin")(apiMediaCatalogReview()))
	mux.HandleFunc("POST /api/admin/media-center/catalog/delete", requireRole("admin")(apiMediaCatalogDelete()))
	mux.HandleFunc("POST /api/admin/media-center/catalog/import", requireRole("admin")(apiMediaCatalogImport()))
	mux.HandleFunc("POST /api/admin/media-center/catalog/seed", requireRole("admin")(apiMediaCatalogSeed()))
	mux.HandleFunc("POST /api/admin/media-center/snapshots/rebuild", requireRole("admin")(apiMediaSnapshotsRebuild()))
	mux.HandleFunc("GET /api/admin/media-center/curation", requireRole("admin")(apiMediaCurationGet()))
	mux.HandleFunc("PUT /api/admin/media-center/curation", requireRole("admin")(apiMediaCurationSave()))
	mux.HandleFunc("GET /api/admin/media-center/app-release", requireRole("admin")(apiMediaAppReleaseGet()))
	mux.HandleFunc("POST /api/admin/media-center/app-release", requireRole("admin")(apiMediaAppReleaseUpload()))
	// IP block management (Go-level, works behind Cloudflare)
	mux.HandleFunc("GET /api/admin/security/blocked", requireRole("admin")(apiBlockedIPs()))
	mux.HandleFunc("POST /api/admin/security/block", requireRole("admin")(apiBlockIP()))
	mux.HandleFunc("POST /api/admin/security/unblock", requireRole("admin")(apiUnblockIP()))

	// Health check (no auth required)
	mux.HandleFunc("GET /api/admin/health", func(w http.ResponseWriter, _ *http.Request) {
		if err := runtimeFilesReady(); err != nil {
			log.Printf("health check failed: %v", err)
			writeError(w, "Not ready", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// Claude Code downloads — admin manages, viewer can see + download stored
	mux.HandleFunc("GET /api/admin/claude-code/releases", requireRole("admin")(apiClaudeCodeReleases()))
	mux.HandleFunc("POST /api/admin/claude-code/refresh", requireRole("admin")(apiClaudeCodeRefresh()))
	mux.HandleFunc("GET /api/admin/claude-code/platforms/{version}", requireRole("admin")(apiClaudeCodePlatforms()))
	mux.HandleFunc("GET /api/admin/claude-code/stored", requireRole("viewer")(apiClaudeCodeStored()))
	mux.HandleFunc("POST /api/admin/claude-code/download", requireRole("admin")(apiClaudeCodeDownload()))
	mux.HandleFunc("GET /api/admin/claude-code/downloads/{filename}", requireRole("viewer")(apiClaudeCodeServeDownload()))
	mux.HandleFunc("DELETE /api/admin/claude-code/downloads", requireRole("admin")(apiClaudeCodeDeleteDownload()))
	mux.HandleFunc("DELETE /api/admin/claude-code/downloads/", requireRole("admin")(apiClaudeCodeDeleteDownload()))
	// Service logs
	mux.HandleFunc("GET /api/admin/services/{name}/logs", requireRole("admin")(apiServiceLogs(cli)))
	mux.HandleFunc("GET /api/admin/services/{name}/logs/stream", requireRole("admin")(apiServiceLogStream(cli)))
	// Xray downloads — admin manages, viewer can see + download stored
	mux.HandleFunc("GET /api/admin/xray/releases", requireRole("admin")(apiXrayReleases()))
	mux.HandleFunc("POST /api/admin/xray/refresh", requireRole("admin")(apiXrayRefresh()))
	mux.HandleFunc("GET /api/admin/xray/platforms/{version}", requireRole("admin")(apiXrayPlatforms()))
	mux.HandleFunc("GET /api/admin/xray/stored", requireRole("viewer")(apiXrayStored()))
	mux.HandleFunc("POST /api/admin/xray/download", requireRole("admin")(apiXrayDownload()))
	mux.HandleFunc("GET /api/admin/xray/downloads/{filename}", requireRole("viewer")(apiXrayServeDownload()))
	mux.HandleFunc("DELETE /api/admin/xray/downloads", requireRole("admin")(apiXrayDeleteDownload()))
	mux.HandleFunc("DELETE /api/admin/xray/downloads/", requireRole("admin")(apiXrayDeleteDownload()))
	// Rate limit management (admin only)
	mux.HandleFunc("GET /api/admin/security/rate-limit", requireRole("admin")(apiRateLimitStatus()))
	mux.HandleFunc("POST /api/admin/security/rate-limit/unblock", requireRole("admin")(apiRateLimitUnblock()))
	// File transfer station — viewer+ uploads/lists, public download via share code
	mux.HandleFunc("POST /api/admin/transfer/upload", requireRole("viewer")(apiTransferUpload()))
	mux.HandleFunc("GET /api/admin/transfer/files", requireRole("viewer")(apiTransferList()))
	mux.HandleFunc("DELETE /api/admin/transfer/files", requireRole("viewer")(apiTransferDelete()))
	// Public transfer download — no auth required
	mux.HandleFunc("GET /api/transfer/{code}", apiTransferPublicDownload())

	// Svelte SPA - serve at /admin/ (any subpath serves index.html)
	mux.HandleFunc("/admin/", serveSPA)
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
		if r.URL.Path != "/api/admin/transfer/upload" &&
			r.URL.Path != "/api/admin/media-center/app-release" &&
			r.URL.Path != "/api/admin/media-center/catalog/import" {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB max request body
		}
		// Static assets: cache forever (fingerprinted filenames), skip rate limit
		if isStaticAsset(r.URL.Path) {
			w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
			ipBlockMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
				mux.ServeHTTP(w2, r2)
			})(w, r)
			return
		}

		rateLimitMiddleware(ipBlockMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
			mux.ServeHTTP(w2, r2)
		}))(w, r)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	authRateLimiter.start()

	// Graceful shutdown — context 创建在 cleanup goroutine 之前，
	// 这样 transferCleanupLoop 能响应 SIGTERM 信号优雅退出。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go transferCleanupLoop(ctx)

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
				log.Printf("[startup] panic in startupRotateIPv6: %v", r)
			}
		}()
		time.Sleep(10 * time.Second)
		startupRotateIPv6(cli)
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

func serveSPA(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/app/ui/index.html")
}

// isStaticAsset returns true for fingerprinted asset paths that bypass rate limiting.
func isStaticAsset(path string) bool {
	return len(path) >= 8 &&
		(path == "/favicon.svg" || path == "/icons.svg" ||
			(len(path) > 8 && path[:8] == "/assets/"))
}
