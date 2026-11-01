package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

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
	containerRefRe   = regexp.MustCompile(versioned + `/containers/([^/]+)/`)
	execRefRe        = regexp.MustCompile(versioned + `/exec/([^/]+)/`)
	execCreatePathRe = regexp.MustCompile(versioned + `/containers/[^/]+/exec$`)
)

// validateExecRequest 限制 exec 创建请求的字段。白名单放行 exec 是为了让
// admin 能跑日志/统计/ip 命令，但 ExecOptions 还允许 Privileged、User、
// Env、WorkingDir 等载荷：一旦放开，能连上本代理的主体就能在目标容器里
// 以任意用户/特权/环境执行，等价容器 root。admin 侧只用 Cmd +
// AttachStdout/AttachStderr，其余字段一律拒绝。
func validateExecRequest(w http.ResponseWriter, r *http.Request) bool {
	const maxExecBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(r.Body, maxExecBody+1))
	if err != nil {
		http.Error(w, "cannot read exec body", http.StatusBadRequest)
		return false
	}
	if len(body) > maxExecBody {
		http.Error(w, "exec body too large", http.StatusRequestEntityTooLarge)
		return false
	}
	var req struct {
		Privileged  bool     `json:"Privileged"`
		User        string   `json:"User"`
		Env         []string `json:"Env"`
		WorkingDir  string   `json:"WorkingDir"`
		AttachStdin bool     `json:"AttachStdin"`
		Detach      bool     `json:"Detach"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid exec json", http.StatusBadRequest)
		return false
	}
	if req.Privileged || req.User != "" || len(req.Env) > 0 || req.WorkingDir != "" || req.AttachStdin || req.Detach {
		log.Printf("denied exec payload %q: privileged/user/env/workdir/stdin/detach not allowed", r.URL.Path)
		http.Error(w, "exec payload contains fields outside the proxy allowlist", http.StatusForbidden)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return true
}

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
		// exec id 不能直接查容器,先取 exec inspect 的容器 ID 再核对归属。
		// 字段名以实测为准(engine 29.x / API v1.51 返回 "ContainerID",
		// 旧版文档作 "Container"),两者都收,空即拒绝。
		status, body, err := inspect("/exec/" + m[1] + "/json")
		if err != nil || status != http.StatusOK {
			log.Printf("scope: exec inspect failed status=%d err=%v (deny)", status, err)
			return false
		}
		var execInfo struct {
			ContainerID string `json:"ContainerID"`
			Container   string `json:"Container"`
		}
		if err := json.Unmarshal(body, &execInfo); err != nil {
			log.Printf("scope: exec inspect parse failed err=%v (deny)", err)
			return false
		}
		target := execInfo.ContainerID
		if target == "" {
			target = execInfo.Container
		}
		if target == "" {
			log.Printf("scope: exec inspect has no container reference (deny)")
			return false
		}
		return containerInAllowedProject(target, inspect)
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

// scopedReadPaths 是"不看具体容器、可跨项目返回数据"的读端点。它们没有容器
// 句柄可供归属校验,唯一能约束作用域的手段是要求调用方自带 compose 项目
// label 过滤(daemon 侧按 AND 语义收敛,见 docker api/types/filters)。
var scopedReadPaths = []*regexp.Regexp{
	regexp.MustCompile(versioned + `/containers/json$`),
	regexp.MustCompile(versioned + `/events$`),
}

const composeProjectLabel = "com.docker.compose.project="

// readScopeOK 报告 handle-less 读端点是否带上了允许项目的 label 过滤。
// fail-closed:filters 缺失、JSON 不可解析、label 不含允许项目一律拒绝。
// 刻意不改写请求(给上游注入 label 会让"代理检查的串"与"上游执行的串"再次
// 分离,且多项目配置下一条 label 过滤无法表达 OR)——缺过滤就是调用方错,
// 让它响亮地 403,而不是静默放行跨项目数据。
func readScopeOK(method, rawPath, rawQuery string) bool {
	if method != http.MethodGet {
		return true
	}
	scoped := false
	for _, re := range scopedReadPaths {
		if re.MatchString(rawPath) {
			scoped = true
			break
		}
	}
	if !scoped {
		return true
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return false
	}
	raw := q.Get("filters")
	if raw == "" {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return false
	}
	for _, v := range filterValues(fields["label"]) {
		if project, ok := strings.CutPrefix(v, composeProjectLabel); ok && allowedProjects[project] {
			return true
		}
	}
	return false
}

// filterValues 解出 filters 中某个 key 的取值集合。docker 客户端发 map 形态
// ({"label":{"k=v":true}}),旧版客户端与手工调用发数组形态({"label":["k=v"]}),
// daemon 两者都接受(api/types/filters.FromJSON 的 legacy 回退分支),这里同样
// 都要认——只认一种会让另一种合法调用被误拒。
func filterValues(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var set map[string]bool
	if err := json.Unmarshal(raw, &set); err == nil {
		out := make([]string, 0, len(set))
		for k, on := range set {
			if on {
				out = append(out, k)
			}
		}
		return out
	}
	return nil
}
