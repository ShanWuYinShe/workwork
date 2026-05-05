package main

import (
	"fmt"
	"log"
	"net/http"

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

	// Dashboard — viewer+
	mux.HandleFunc("GET /api/admin/dashboard", requireRole("viewer")(apiDashboard(cli)))
	mux.HandleFunc("GET /api/admin/services", requireRole("viewer")(apiServices(cli)))
	mux.HandleFunc("GET /api/admin/config", requireRole("viewer")(apiConfig(cli)))

	// User operations — viewer+ (read own), admin (manage all)
	mux.HandleFunc("GET /api/admin/users", requireRole("viewer")(apiUsers(cli)))
	mux.HandleFunc("POST /api/admin/users/password", requireRole("viewer")(apiChangePassword(cli)))

	// Admin-only operations
	mux.HandleFunc("POST /api/admin/services/{name}/{action}", requireRole("admin")(apiServiceAction(cli)))
	mux.HandleFunc("POST /api/admin/users/xray/add", requireRole("admin")(apiAddXray(cli)))
	mux.HandleFunc("POST /api/admin/users/xray/delete", requireRole("admin")(apiDeleteXray(cli)))
	mux.HandleFunc("POST /api/admin/users/admin/add", requireRole("admin")(apiAddAdmin(cli)))
	mux.HandleFunc("POST /api/admin/users/admin/delete", requireRole("admin")(apiDeleteAdmin(cli)))
	mux.HandleFunc("POST /api/admin/users/roles/set", requireRole("admin")(apiSetRole(cli)))
	mux.HandleFunc("GET /api/admin/network/status", requireRole("admin")(apiNetworkStatus(cli)))
	mux.HandleFunc("POST /api/admin/network/rotate-ipv6", requireRole("admin")(apiRotateIPv6(cli)))
	mux.HandleFunc("POST /api/admin/network/sync-dns", requireRole("admin")(apiSyncDNS(cli)))
	mux.HandleFunc("GET /api/admin/security/jails", requireRole("admin")(apiJails(cli)))
	mux.HandleFunc("POST /api/admin/security/blacklist/ban", requireRole("admin")(apiBanIP(cli)))
	mux.HandleFunc("POST /api/admin/security/blacklist/unban", requireRole("admin")(apiUnbanIP(cli)))
	mux.HandleFunc("GET /api/admin/cliproxy/instances", requireRole("admin")(apiCliproxyInstances(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/start", requireRole("admin")(apiCliproxyStart(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/stop", requireRole("admin")(apiCliproxyStop(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/get-config", requireRole("admin")(apiCliproxyGetConfig(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/set-config", requireRole("admin")(apiCliproxySetConfig(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/codex-login", requireRole("admin")(apiCliproxyCodexLogin(cli)))
	mux.HandleFunc("POST /api/admin/cliproxy/upload-oauth", requireRole("admin")(apiCliproxyUploadOAuth(cli)))

	// Svelte SPA - serve at /admin/ (any subpath serves index.html)
	mux.HandleFunc("/admin/", serveSPA)
	mux.HandleFunc("GET /admin", serveSPA)

	// Svelte static assets - serve at absolute paths
	static := http.FileServer(http.Dir("/app/ui"))
	mux.Handle("/assets/", static)
	mux.Handle("/favicon.svg", static)
	mux.Handle("/icons.svg", static)

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Admin panel listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/app/ui/index.html")
}
