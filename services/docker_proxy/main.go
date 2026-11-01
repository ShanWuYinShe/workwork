package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

var version = "dev"

func main() {
	socket := os.Getenv("DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = ":2375"
	}

	rp := newReverseProxy()
	http.HandleFunc("/", newHandler(rp))

	srv := &http.Server{
		Addr:              listen,
		Handler:           nil, // DefaultServeMux
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("workwork-docker-proxy %s listening on %s (socket=%s, %d whitelist rules, allowed compose projects: %v)",
		version, listen, socket, len(whitelist), allowedProjects)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
