package main

import "testing"

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
