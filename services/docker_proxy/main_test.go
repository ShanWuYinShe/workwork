package main

import (
	"context"
	"testing"
)

// TestAllowed 锁定白名单边界:admin 调用面放行,create/delete/images 等
// 越权端点(含 API 版本前缀形态)一律拒绝。
func TestAllowed(t *testing.T) {
	allowedReqs := []struct{ method, path string }{
		{"GET", "/_ping"},
		{"HEAD", "/_ping"},
		{"HEAD", "/v1.47/_ping"},
		{"GET", "/version"},
		{"GET", "/v1.47/version"},
		{"GET", "/v1.47/info"},
		{"GET", "/v1.47/events"},
		{"GET", "/v1.47/containers/json"},
		{"GET", "/v1.47/containers/abc123/json"},
		{"GET", "/v1.47/containers/abc123/logs?tail=100&follow=true"},
		{"GET", "/v1.47/exec/e123/json"},
		{"POST", "/v1.47/containers/abc123/start"},
		{"POST", "/v1.47/containers/abc123/stop"},
		{"POST", "/v1.47/containers/abc123/restart"},
		{"POST", "/v1.47/containers/abc123/exec"},
		{"POST", "/v1.47/exec/e123/start"},
		// 无版本前缀的原始路径
		{"GET", "/containers/json"},
		{"POST", "/containers/abc123/restart"},
	}
	for _, r := range allowedReqs {
		if !allowed(r.method, r.path) {
			t.Errorf("should be allowed: %s %s", r.method, r.path)
		}
	}

	deniedReqs := []struct{ method, path string }{
		// 容器生命周期扩展:创建/删除即逃逸面(bind-mount + privileged)
		{"POST", "/v1.47/containers/create"},
		{"DELETE", "/v1.47/containers/abc123"},
		{"GET", "/v1.47/containers/create"},
		// 镜像/网络/卷/swarm 全拒
		{"GET", "/v1.47/images/json"},
		{"GET", "/v1.47/networks"},
		{"GET", "/v1.47/volumes"},
		{"GET", "/v1.47/swarm"},
		{"POST", "/v1.47/images/create?fromImage=alpine"},
		{"POST", "/v1.47/networks/net1/connect"},
		{"POST", "/v1.47/build"},
		{"DELETE", "/v1.47/volumes/vol1"},
		// exec resize 等未盘点端点
		{"POST", "/v1.47/exec/e123/resize"},
		{"GET", "/v1.47/containers/abc123/stats"},
		{"GET", "/v1.47/containers/abc123/archive?path=/etc"},
		{"PUT", "/v1.47/containers/abc123/archive"},
		// 相似前缀混淆
		{"GET", "/v1.47/containers/jsonx"},
		{"GET", "/v1.47/containersxyz/json"},
		{"GET", "/versionx"},
		{"GET", "/x/version"},
	}
	for _, r := range deniedReqs {
		if allowed(r.method, r.path) {
			t.Errorf("should be DENIED: %s %s", r.method, r.path)
		}
	}
}

func TestParseAllowedProjects(t *testing.T) {
	if got := parseAllowedProjects(""); !got["workwork"] || len(got) != 1 {
		t.Errorf("empty env should default to {workwork}, got %v", got)
	}
	got := parseAllowedProjects("workwork, test ,x")
	if !got["workwork"] || !got["test"] || !got["x"] || len(got) != 3 {
		t.Errorf("comma split with trim failed, got %v", got)
	}
}

// TestScopeCheck 锁定容器归属校验:允许项目名下的容器/exec 放行,跨项目、
// 无 label、inspect 失败一律拒绝(fail-closed);无容器句柄的端点(列表、
// events)不触发旁路 inspect。
func TestScopeCheck(t *testing.T) {
	orig := allowedProjects
	defer func() { allowedProjects = orig }()
	allowedProjects = map[string]bool{"workwork": true}

	containerJSON := func(project string) string {
		if project == "" {
			return `{"Config":{"Labels":{}}}`
		}
		return `{"Config":{"Labels":{"com.docker.compose.project":"` + project + `"}}}`
	}
	static := func(status int, body string, err error) inspectFunc {
		return func(string) (int, []byte, error) { return status, []byte(body), err }
	}

	cases := []struct {
		name    string
		path    string
		inspect inspectFunc
		want    bool
	}{
		{"本项目容器 logs", "/v1.47/containers/abc/logs?tail=5",
			static(200, containerJSON("workwork"), nil), true},
		{"本项目容器 exec 创建", "/v1.47/containers/abc/exec",
			static(200, containerJSON("workwork"), nil), true},
		{"跨项目容器拒绝", "/v1.47/containers/abc/restart",
			static(200, containerJSON("music_player"), nil), false},
		{"无 project label 拒绝", "/containers/abc/start",
			static(200, containerJSON(""), nil), false},
		{"inspect 404 拒绝", "/containers/abc/stop",
			static(404, `{"message":"no such container"}`, nil), false},
		{"inspect 出错拒绝", "/containers/abc/json",
			static(0, "", context.DeadlineExceeded), false},
	}
	for _, tc := range cases {
		if got := scopeCheck(tc.path, tc.inspect); got != tc.want {
			t.Errorf("%s: scopeCheck=%v want %v", tc.name, got, tc.want)
		}
	}

	// 无容器句柄的端点直接放行且不得触发 inspect。
	noInspect := func(string) (int, []byte, error) {
		t.Error("list/events endpoints must not trigger inspect")
		return 500, nil, nil
	}
	for _, path := range []string{"/v1.47/containers/json", "/v1.47/events", "/_ping"} {
		if !scopeCheck(path, noInspect) {
			t.Errorf("%s: handle-free endpoint should pass scope check", path)
		}
	}

	// exec 两段链:exec inspect 的容器字段再查容器归属。字段名取自
	// engine 29.x 实测响应("ContainerID"),并兼容旧文档的 "Container"。
	execChain := func(containerBody string) inspectFunc {
		return func(apiPath string) (int, []byte, error) {
			if apiPath == "/exec/e123/json" {
				return 200, []byte(`{"ContainerID":"abc","Running":false}`), nil
			}
			return 200, []byte(containerBody), nil
		}
	}
	execChainLegacy := func(containerBody string) inspectFunc {
		return func(apiPath string) (int, []byte, error) {
			if apiPath == "/exec/e123/json" {
				return 200, []byte(`{"Container":"abc"}`), nil
			}
			return 200, []byte(containerBody), nil
		}
	}
	// 两个键都缺失 = 无容器引用,必须拒绝。
	execChainNoRef := func(apiPath string) (int, []byte, error) {
		return 200, []byte(`{"Running":false}`), nil
	}
	if !scopeCheck("/v1.47/exec/e123/start", execChain(containerJSON("workwork"))) {
		t.Error("exec on workwork container should be allowed")
	}
	if scopeCheck("/v1.47/exec/e123/start", execChain(containerJSON("other"))) {
		t.Error("exec on cross-project container should be denied")
	}
	if !scopeCheck("/v1.47/exec/e123/start", execChainLegacy(containerJSON("workwork"))) {
		t.Error("legacy Container key should be accepted")
	}
	if scopeCheck("/v1.47/exec/e123/start", execChainNoRef) {
		t.Error("exec inspect without container reference should be denied")
	}
}
