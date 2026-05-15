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

	// Auto-generate configs from templates on first start
	if err := generateConfigs(); err != nil {
		log.Printf("WARNING: config generation: %v", err)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}

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
	// IP block management (Go-level, works behind Cloudflare)
	mux.HandleFunc("GET /api/admin/security/blocked", requireRole("admin")(apiBlockedIPs()))
	mux.HandleFunc("POST /api/admin/security/block", requireRole("admin")(apiBlockIP()))
	mux.HandleFunc("POST /api/admin/security/unblock", requireRole("admin")(apiUnblockIP()))
	mux.HandleFunc("GET /api/admin/cliproxy/instances", requireRole("admin")(apiCliproxyInstances(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/start", requireRole("admin")(apiCliproxyStart(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/stop", requireRole("admin")(apiCliproxyStop(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/get-config", requireRole("admin")(apiCliproxyGetConfig(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/set-config", requireRole("admin")(apiCliproxySetConfig(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/codex-login", requireRole("admin")(apiCliproxyCodexLogin(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/upload-oauth", requireRole("admin")(apiCliproxyUploadOAuth(cli)))
	mux.HandleFunc("GET /api/admin/cliproxy/public-config", requireRole("admin")(apiCliproxyPublicConfig(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/public-config", requireRole("admin")(apiCliproxyPublicConfig(cli)))

	// Health check (no auth required)
	mux.HandleFunc("GET /api/admin/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})

	// Model pool management
	mux.HandleFunc("GET /api/admin/model-pool", requireRole("admin")(apiModelPool(cli)))
	mux.HandleFunc("POST /api/admin/model-pool", requireRole("admin")(apiModelPool(cli)))
	mux.HandleFunc("POST /api/admin/model-pool/users", requireRole("admin")(apiModelPoolUsers(cli)))
	// Claude Code downloads — admin manages, viewer can see + download stored
	mux.HandleFunc("GET /api/admin/claude-code/releases", requireRole("admin")(apiClaudeCodeReleases()))
	mux.HandleFunc("POST /api/admin/claude-code/refresh", requireRole("admin")(apiClaudeCodeRefresh()))
	mux.HandleFunc("GET /api/admin/claude-code/platforms/{version}", requireRole("admin")(apiClaudeCodePlatforms()))
	mux.HandleFunc("GET /api/admin/claude-code/stored", requireRole("viewer")(apiClaudeCodeStored()))
	mux.HandleFunc("POST /api/admin/claude-code/download", requireRole("admin")(apiClaudeCodeDownload()))
	mux.HandleFunc("GET /api/admin/claude-code/downloads/{filename}", requireRole("viewer")(apiClaudeCodeServeDownload()))
	mux.HandleFunc("DELETE /api/admin/claude-code/downloads/", requireRole("admin")(apiClaudeCodeDeleteDownload()))
	// Service logs
	mux.HandleFunc("GET /api/admin/services/{name}/logs", requireRole("admin")(apiServiceLogs(cli)))
	// Rate limit management (admin only)
	mux.HandleFunc("GET /api/admin/security/rate-limit", requireRole("admin")(apiRateLimitStatus()))
	mux.HandleFunc("POST /api/admin/security/rate-limit/unblock", requireRole("admin")(apiRateLimitUnblock()))

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
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB max request body

		// Static assets: cache forever (fingerprinted filenames), skip rate limit
		if isStaticAsset(r.URL.Path) {
			w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
			ipBlockMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
				mux.ServeHTTP(w2, r2)
			})(w, r)
			return
		}

		ipBlockMiddleware(rateLimitMiddleware(func(w2 http.ResponseWriter, r2 *http.Request) {
			mux.ServeHTTP(w2, r2)
		}))(w, r)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	authRateLimiter.start()

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("Admin panel listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
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
