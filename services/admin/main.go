package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"admin/handlers"
	"admin/middleware"

	"github.com/docker/docker/client"
)

func main() {
	port := os.Getenv("ADMIN_PORT")
	if port == "" {
		port = "8318"
	}

	teamDomain := os.Getenv("CF_TEAM_DOMAIN")
	policyAUD := os.Getenv("CF_POLICY_AUD")
	if teamDomain == "" || policyAUD == "" {
		log.Println("WARNING: CF_TEAM_DOMAIN or CF_POLICY_AUD not set, auth middleware disabled")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /admin/", handlers.Dashboard(cli))
	mux.HandleFunc("GET /admin/services", handlers.ServicesList(cli))
	mux.HandleFunc("POST /admin/services/{name}/start", handlers.ServiceStart(cli))
	mux.HandleFunc("POST /admin/services/{name}/stop", handlers.ServiceStop(cli))
	mux.HandleFunc("POST /admin/services/{name}/restart", handlers.ServiceRestart(cli))
	mux.HandleFunc("GET /admin/services/{name}/logs", handlers.ServiceLogsPage(cli))
	mux.HandleFunc("GET /admin/api/services/{name}/logs", handlers.ServiceLogs(cli))

	var handler http.Handler = mux
	if teamDomain != "" && policyAUD != "" {
		handler = middleware.CFAuth(teamDomain, policyAUD)(mux)
	}

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Admin panel listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
