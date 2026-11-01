package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

var upstreamTransport = &http.Transport{
	DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		socket := os.Getenv("DOCKER_SOCKET")
		if socket == "" {
			socket = "/var/run/docker.sock"
		}
		return d.DialContext(ctx, "unix", socket)
	},
	IdleConnTimeout: 90 * time.Second,
}

// inspectClient 只服务归属校验的旁路 GET；必须有超时，daemon 卡死时快速 deny。
var inspectClient = &http.Client{Transport: upstreamTransport, Timeout: 10 * time.Second}

func daemonInspect(apiPath string) (int, []byte, error) {
	resp, err := inspectClient.Get("http://docker" + apiPath)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func newReverseProxy() *httputil.ReverseProxy {
	upstream := &url.URL{Scheme: "http", Host: "docker"} // Host 仅占位,连接走 DialContext
	return &httputil.ReverseProxy{
		Transport: upstreamTransport,
		// events/logs/exec 流式响应立即冲刷,不做缓冲。
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.URL.Path = pr.In.URL.Path
			pr.Out.URL.RawPath = pr.In.URL.RawPath
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error %s %s: %v", r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

// newHandler 是 docker API 白名单/归属/exec 载荷校验的唯一入口。
func newHandler(rp *httputil.ReverseProxy) http.HandlerFunc {
	projects := make([]string, 0, len(allowedProjects))
	for p := range allowedProjects {
		projects = append(projects, p)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// %q:URL.Path 是一次解码后的字符串,%0A 换行会伪造日志行。
		if !allowed(r.Method, r.URL.Path) {
			log.Printf("denied %s %q (not in whitelist)", r.Method, r.URL.Path)
			http.Error(w, "endpoint not allowed", http.StatusForbidden)
			return
		}
		if !scopeCheck(r.URL.Path, daemonInspect) {
			log.Printf("denied %s %q (target container out of allowed compose projects %v)", r.Method, r.URL.Path, projects)
			http.Error(w, "container not in allowed project", http.StatusForbidden)
			return
		}
		if !readScopeOK(r.Method, r.URL.Path, r.URL.RawQuery) {
			log.Printf("denied %s %q (unscoped list/events: missing allowed compose project label filter)", r.Method, r.URL.Path)
			http.Error(w, "list/events must be scoped to an allowed compose project", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && execCreatePathRe.MatchString(r.URL.Path) {
			if !validateExecRequest(w, r) {
				return
			}
		}
		log.Printf("allowed %s %q", r.Method, r.URL.Path)
		rp.ServeHTTP(w, r)
	}
}
