package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/docker/docker/client"
)

func main() {
	port := os.Getenv("ADMIN_PORT")
	if port == "" {
		port = "8318"
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}

	mux := http.NewServeMux()

	// JSON API
	mux.HandleFunc("GET /api/admin/dashboard", apiDashboard(cli))
	mux.HandleFunc("GET /api/admin/services", apiServices(cli))
	mux.HandleFunc("POST /api/admin/services/{name}/{action}", apiServiceAction(cli))
	mux.HandleFunc("GET /api/admin/config", apiConfig(cli))
	mux.HandleFunc("GET /api/admin/users", apiUsers(cli))
	mux.HandleFunc("POST /api/admin/users/xray/add", apiAddXray(cli))
	mux.HandleFunc("POST /api/admin/users/xray/delete", apiDeleteXray(cli))
	mux.HandleFunc("POST /api/admin/users/password", apiChangePassword(cli))
	mux.HandleFunc("POST /api/admin/users/admin/add", apiAddAdmin(cli))
	mux.HandleFunc("POST /api/admin/users/admin/delete", apiDeleteAdmin(cli))

	// Svelte SPA - serve at /admin/ (any subpath serves index.html)
	mux.HandleFunc("/admin/", serveSPA)
	mux.HandleFunc("GET /admin", serveSPA)

	// Svelte static assets
	spa := http.FileServer(http.Dir("/app/ui"))
	mux.Handle("/ui/", http.StripPrefix("/ui/", spa))

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Admin panel listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "/app/ui/index.html")
}
