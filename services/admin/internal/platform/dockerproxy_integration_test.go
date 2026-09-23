package platform

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// TestDockerProxyWhitelist 验证 workwork-docker-proxy 白名单恰好覆盖 admin
// 的 docker API 调用面,且越权端点(containers create/delete/images/networks/
// volumes/swarm)被拒。需真实 proxy(仓库根构建后):
//
//	docker build -t workwork-docker-proxy services/docker_proxy
//	docker run -d --name wk-proxy-test -p 127.0.0.1:23750:2375 \
//	  -e ALLOWED_COMPOSE_PROJECTS=test \
//	  -v /var/run/docker.sock:/var/run/docker.sock:ro workwork-docker-proxy
//	TEST_DOCKER_HOST=tcp://127.0.0.1:23750 go test -run TestDockerProxyWhitelist ./internal/platform/
//
// 未设置 TEST_DOCKER_HOST 时跳过(CI 无 docker daemon 可供 proxy 代理)。
func TestDockerProxyWhitelist(t *testing.T) {
	proxyHost := os.Getenv("TEST_DOCKER_HOST")
	if proxyHost == "" {
		t.Skip("TEST_DOCKER_HOST not set; run against a docker-socket-proxy locally")
	}

	// 经 proxy 的 client(admin 的连接方式)。
	t.Setenv("DOCKER_HOST", proxyHost)
	proxyCli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("negotiate via proxy: %v", err)
	}
	defer proxyCli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 直连 daemon 准备一个测试容器(admin 不 create 容器,create 不在白名单)。
	// 显式 unix socket:上面的 t.Setenv 会污染 FromEnv。
	localCli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"))
	if err != nil {
		t.Fatalf("local daemon client: %v", err)
	}
	defer localCli.Close()
	resp, err := localCli.ContainerCreate(ctx, &container.Config{
		Image: "alpine:3.24.1",
		Cmd:   []string{"sh", "-c", "echo hello-from-exec; sleep 30"},
		Tty:   true,
		// 归属校验(proxy 端 ALLOWED_COMPOSE_PROJECTS)只放行名单内项目,
		// 测试容器必须落在 label 圈定的项目里才能走通执行类端点。
		Labels: map[string]string{"com.docker.compose.project": "test"},
	}, nil, nil, nil, "wk-proxy-whitelist-test")
	if err != nil {
		t.Fatalf("create test container via local daemon: %v", err)
	}
	defer func() {
		rmc, rmCancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = localCli.ContainerRemove(rmc, resp.ID, container.RemoveOptions{Force: true})
		rmCancel()
	}()
	if err := localCli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("start test container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = localCli.ContainerStop(stopCtx, resp.ID, container.StopOptions{})
		stopCancel()
	})

	// ——— admin 调用面逐项验证 ———
	// list(Filter with labels — ComposeProjectFilters 的形态)
	listed, err := proxyCli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(filters.KeyValuePair{
			Key: "label", Value: "com.docker.compose.project=workwork",
		}),
	})
	if err != nil {
		t.Fatalf("ContainerList via proxy: %v", err)
	}
	_ = listed
	if _, err := proxyCli.ContainerInspect(ctx, resp.ID); err != nil {
		t.Fatalf("ContainerInspect via proxy: %v", err)
	}
	logs, err := proxyCli.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true, Tail: "10"})
	if err != nil {
		t.Fatalf("ContainerLogs via proxy: %v", err)
	}
	logs.Close()
	if _, err := proxyCli.Info(ctx); err != nil {
		t.Fatalf("Info via proxy: %v", err)
	}
	// events 流(读一帧或超时即算通,IPWatcher 只需要订阅不报错)。
	ctxE, cancelE := context.WithTimeout(ctx, 3*time.Second)
	_, errCh := proxyCli.Events(ctxE, events.ListOptions{})
	select {
	case err := <-errCh:
		t.Fatalf("Events via proxy: %v", err)
	case <-ctxE.Done():
	}
	cancelE()

	// exec create+start+inspect(ip addr add / nginx -s reload 的路径)。
	execCfg := container.ExecOptions{
		Cmd:          []string{"sh", "-c", "echo proxy-exec-ok"},
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := proxyCli.ContainerExecCreate(ctx, resp.ID, execCfg)
	if err != nil {
		t.Fatalf("ContainerExecCreate via proxy: %v", err)
	}
	attach, err := proxyCli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		t.Fatalf("ContainerExecAttach via proxy: %v", err)
	}
	attach.Close()
	for i := 0; i < 20; i++ {
		insp, err := proxyCli.ContainerExecInspect(ctx, execResp.ID)
		if err != nil {
			t.Fatalf("ContainerExecInspect via proxy: %v", err)
		}
		if !insp.Running {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 归属校验拒绝路径:无 compose 项目 label 的容器,per-container 端点
	// 一律 403(与白名单 403 同形,SDK 侧表现为错误返回)。
	deniedResp, err := localCli.ContainerCreate(ctx, &container.Config{
		Image: "alpine:3.24.1",
		Cmd:   []string{"sh", "-c", "sleep 30"},
		Tty:   true,
	}, nil, nil, nil, "wk-proxy-scope-denied-test")
	if err == nil {
		t.Cleanup(func() {
			rmCtx, rmCancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = localCli.ContainerRemove(rmCtx, deniedResp.ID, container.RemoveOptions{Force: true})
			rmCancel()
		})
		if _, err := proxyCli.ContainerInspect(ctx, deniedResp.ID); err == nil {
			t.Error("inspect on out-of-project container should be denied by proxy scope check")
		}
	}

	// start/stop/restart 放行(ApiServiceAction)。
	if err := proxyCli.ContainerStop(ctx, resp.ID, container.StopOptions{}); err != nil {
		t.Fatalf("ContainerStop via proxy: %v", err)
	}
	if err := proxyCli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("ContainerStart via proxy: %v", err)
	}
	if err := proxyCli.ContainerRestart(ctx, resp.ID, container.StopOptions{}); err != nil {
		t.Fatalf("ContainerRestart via proxy: %v", err)
	}

	// ——— 越权端点必须被拒(POST create 是 bind-mount 逃逸面,重点) ———
	createURL := strings.Replace(proxyHost, "tcp://", "http://", 1) + "/v1.47/containers/create"
	if respC, err := http.Post(createURL, "application/json", strings.NewReader(`{"Image":"alpine"}`)); err != nil {
		t.Fatalf("POST containers/create: %v", err)
	} else {
		respC.Body.Close()
		if respC.StatusCode != http.StatusForbidden {
			t.Errorf("POST /containers/create = %d, want 403 (escape vector must be denied)", respC.StatusCode)
		}
	}
	for _, path := range []string{"/images/json", "/networks", "/volumes", "/containers/create?x=1", "/swarm"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyHost+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		proxyHostHTTP := strings.Replace(proxyHost, "tcp://", "http://", 1)
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(proxyHostHTTP, "http://")
		resp2, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s via proxy: %v", path, err)
		}
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s via proxy = %d, want 403 (whitelist must deny)", path, resp2.StatusCode)
		}
	}
}
