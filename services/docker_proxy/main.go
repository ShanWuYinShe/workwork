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
// 在白名单之上还有一层容器归属校验:凡引用具体容器/exec 的端点,转发前经
// GET /containers/{id}/json 核对 com.docker.compose.project label,只放行
// ALLOWED_COMPOSE_PROJECTS(默认 workwork)名下的容器。没有这一层,白名单
// 的 {id} 是全局通配——admin 被攻破即可对宿主上其他 compose 项目的容器
// (如 media/music 栈)exec/启停。残余暴露面:列表与 events 仍跨项目可见
// (仅容器名/镜像等元数据,无执行面),action 强制注入 label 过滤会破坏
// admin 既有查询语义,接受此残余并在日志可审计。
//
// exec attach 是 HTTP Upgrade(hijack)长连接,httputil.ReverseProxy 原生
// 透传;events/logs 流式响应经 FlushInterval=-1 立即冲刷。上游是 unix
// socket,仅监听容器内网(2375),不经公网。
package main

import (
	"context"
	"encoding/json"
	"io"
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

// allowed 报告 (method, path) 是否在白名单内。path 必须是 net/http 解码一次
// 后的 r.URL.Path——与转发侧(ReverseProxy Rewrite 原样透传 In.URL.Path)是
// 同一字节串,严禁在此再做 url.PathUnescape:二次解码会让"检查的串"与
// "上游执行的串"不一致(%252F 形态),是典型的白名单变体绕过模式。
func allowed(method, rawPath string) bool {
	// query 串剥掉后匹配;docker API 语义由 path 表达(filters 走 query)。
	if idx := strings.IndexByte(rawPath, '?'); idx >= 0 {
		rawPath = rawPath[:idx]
	}
	for _, w := range whitelist {
		if w.method == method && w.path.MatchString(rawPath) {
			return true
		}
	}
	return false
}

// 引用具体容器/exec 的路径(要求第二段后有 "/",/containers/json 列表不受影响)。
var (
	containerRefRe = regexp.MustCompile(versioned + `/containers/([^/]+)/`)
	execRefRe      = regexp.MustCompile(versioned + `/exec/([^/]+)/`)
)

// allowedProjects 是放行的 com.docker.compose.project label 集合。
var allowedProjects = parseAllowedProjects(os.Getenv("ALLOWED_COMPOSE_PROJECTS"))

func parseAllowedProjects(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "workwork"
	}
	set := make(map[string]bool)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			set[p] = true
		}
	}
	return set
}

// inspectJSON 经 unix socket 向 docker daemon 发起代理自身的 GET,返回
// (status, body)。抽出成函数便于单测注入。
type inspectFunc func(apiPath string) (int, []byte, error)

// scopeCheck 报告请求目标是否落在允许的 compose 项目内。无容器句柄的端点
// (列表/events/ping 等)直接放行。fail-closed:inspect 失败/容器不存在/
// label 缺失或不匹配一律拒绝,不能因旁路校验失败而放行执行类端点。
func scopeCheck(rawPath string, inspect inspectFunc) bool {
	if m := containerRefRe.FindStringSubmatch(rawPath); m != nil {
		return containerInAllowedProject(m[1], inspect)
	}
	if m := execRefRe.FindStringSubmatch(rawPath); m != nil {
		// exec id 不能直接查容器,先取 /exec/{id}/json 的 .Container。
		status, body, err := inspect("/exec/" + m[1] + "/json")
		if err != nil || status != http.StatusOK {
			log.Printf("scope: exec inspect failed status=%d err=%v (deny)", status, err)
			return false
		}
		var execInfo struct {
			Container string `json:"Container"`
		}
		if err := json.Unmarshal(body, &execInfo); err != nil || execInfo.Container == "" {
			log.Printf("scope: exec inspect parse failed err=%v (deny)", err)
			return false
		}
		return containerInAllowedProject(execInfo.Container, inspect)
	}
	return true
}

func containerInAllowedProject(id string, inspect inspectFunc) bool {
	status, body, err := inspect("/containers/" + id + "/json")
	if err != nil {
		log.Printf("scope: container %q inspect error: %v (deny)", id, err)
		return false
	}
	if status != http.StatusOK {
		// 404=容器已消失:后续操作注定失败,同样拒绝并留痕。
		log.Printf("scope: container %q inspect status=%d (deny)", id, status)
		return false
	}
	var info struct {
		Config *struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(body, &info); err != nil || info.Config == nil {
		log.Printf("scope: container %q inspect parse failed err=%v (deny)", id, err)
		return false
	}
	project := info.Config.Labels["com.docker.compose.project"]
	if !allowedProjects[project] {
		log.Printf("scope: container %q project=%q not in allowed set (deny)", id, project)
		return false
	}
	return true
}

var upstreamTransport = &http.Transport{
	DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		socket := os.Getenv("DOCKER_SOCKET")
		if socket == "" {
			socket = "/var/run/docker.sock"
		}
		return d.DialContext(ctx, "unix", socket)
	},
	// hijack(exec attach)复用 DialContext;空闲连接及时回收。
	IdleConnTimeout: 90 * time.Second,
}

// inspectClient 只服务归属校验的旁路 GET;必须有超时,绝不能像数据面一样
// 长连接无超时——daemon 卡死时校验快速失败(deny),而不是把请求挂住。
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
	proxy := &httputil.ReverseProxy{
		Transport: upstreamTransport,
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

	projects := make([]string, 0, len(allowedProjects))
	for p := range allowedProjects {
		projects = append(projects, p)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(r.Method, r.URL.Path) {
			log.Printf("denied %s %s (not in whitelist)", r.Method, r.URL.Path)
			http.Error(w, "endpoint not allowed", http.StatusForbidden)
			return
		}
		if !scopeCheck(r.URL.Path, daemonInspect) {
			log.Printf("denied %s %s (target container out of allowed compose projects %v)", r.Method, r.URL.Path, projects)
			http.Error(w, "container not in allowed project", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           nil, // DefaultServeMux
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("workwork-docker-proxy %s listening on %s (socket=%s, %d whitelist rules, allowed compose projects: %v)",
		version, listen, socket, len(whitelist), projects)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
