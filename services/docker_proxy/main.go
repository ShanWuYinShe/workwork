// workwork-docker-proxy — docker API 精确白名单反代。
//
// 背景:admin 面板此前直挂 /var/run/docker.sock,全权等价宿主 root。第三方
// docker-socket-proxy(tecnativa)的 CONTAINERS=1 粒度放行整个 /containers
// 前缀——含 POST /containers/create(任意 bind-mount + privileged 即逃逸)
// 与 DELETE,白名单形同虚设。这里按 admin 的真实调用面(盘点见
// services/admin/internal/platform/dockerproxy_integration_test.go)做
// method+path 级白名单,白名单外一律 403:
//
//	GET  /_ping /version /info /events
//	GET  /containers/json | /containers/{id}/json | /containers/{id}/logs
//	GET  /exec/{id}/json
//	POST /containers/{id}/start | /stop | /restart
//	POST /containers/{id}/exec | /exec/{id}/start
//
// exec attach 是 HTTP Upgrade(hijack)长连接,httputil.ReverseProxy 原生
// 透传;events/logs 流式响应经 FlushInterval=-1 立即冲刷。上游是 unix
// socket,仅监听容器内网(2375),不经公网。
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var version = "dev"

// versioned 前缀容忍 docker SDK 的 API 版本路径(/v1.47/...);无前缀形态
// (健康检查、原始 curl)同样放行。
const versioned = `^(?:/v[0-9.]+)?`

// whitelist 按 (method, path) 精确放行;盘点来源见文件头注释。新增 admin
// docker API 调用时必须同步 here——集成测试锁住全部调用路径。
var whitelist = []struct {
	method string
	path   *regexp.Regexp
}{
	{"GET", regexp.MustCompile(versioned + `/_ping$`)},
	// docker SDK Ping() 先 HEAD /_ping 探活,再按需 GET。
	{"HEAD", regexp.MustCompile(versioned + `/_ping$`)},
	{"GET", regexp.MustCompile(versioned + `/version$`)},
	{"GET", regexp.MustCompile(versioned + `/info$`)},
	{"GET", regexp.MustCompile(versioned + `/events$`)},
	{"GET", regexp.MustCompile(versioned + `/containers/json$`)},
	{"GET", regexp.MustCompile(versioned + `/containers/[^/]+/json$`)},
	{"GET", regexp.MustCompile(versioned + `/containers/[^/]+/logs$`)},
	{"GET", regexp.MustCompile(versioned + `/exec/[^/]+/json$`)},
	{"POST", regexp.MustCompile(versioned + `/containers/[^/]+/start$`)},
	{"POST", regexp.MustCompile(versioned + `/containers/[^/]+/stop$`)},
	{"POST", regexp.MustCompile(versioned + `/containers/[^/]+/restart$`)},
	{"POST", regexp.MustCompile(versioned + `/containers/[^/]+/exec$`)},
	{"POST", regexp.MustCompile(versioned + `/exec/[^/]+/start$`)},
}

// allowed 报告 (method, path) 是否在白名单内。
func allowed(method, rawPath string) bool {
	// query 串剥掉后匹配;docker API 语义由 path 表达(filters 走 query)。
	if idx := strings.IndexByte(rawPath, '?'); idx >= 0 {
		rawPath = rawPath[:idx]
	}
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		path = rawPath
	}
	for _, w := range whitelist {
		if w.method == method && w.path.MatchString(path) {
			return true
		}
	}
	return false
}

func main() {
	socket := os.Getenv("DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = ":2375"
	}

	upstream := &url.URL{Scheme: "http", Host: "docker"} // Host 仅占位,连接走 DialContext
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		// hijack(exec attach)复用 DialContext;空闲连接及时回收。
		IdleConnTimeout: 90 * time.Second,
	}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		// events/logs/exec 流式响应立即冲刷,不做缓冲。
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			// docker daemon 不关心 Host,但保留原始路径(已含 /vN.N/ 前缀)。
			pr.Out.URL.Path = pr.In.URL.Path
			pr.Out.URL.RawPath = pr.In.URL.RawPath
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error %s %s: %v", r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(r.Method, r.URL.Path) {
			log.Printf("denied %s %s (not in whitelist)", r.Method, r.URL.Path)
			http.Error(w, "endpoint not allowed", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           nil, // DefaultServeMux
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("workwork-docker-proxy %s listening on %s (socket=%s, %d whitelist rules)",
		version, listen, socket, len(whitelist))
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
