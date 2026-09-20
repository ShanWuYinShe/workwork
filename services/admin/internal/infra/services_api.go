package infra

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"admin/internal/proxy"
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

func ApiServices(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svcs := listServicesJSON(r.Context(), cli)
		platform.WriteJSON(w, map[string]any{"services": svcs})
	}
}

func ApiServiceAction(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		action := r.PathValue("action")

		// 校验服务名格式，仅允许小写字母、数字、下划线、连字符
		if !validServiceName.MatchString(name) {
			platform.WriteError(w, "服务名不合法", 400)
			return
		}
		// 容器操作不随客户端断开而取消:restart 请求发出后若客户端断连,
		// 请求 context 取消可能只完成 stop 未完成 start,服务停在 down 状态。
		// 与 IPv6 轮转/DNS 同步一致,用不受取消影响的 context + 超时。
		opCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), platform.RestartTimeout)
		defer cancel()
		containers, err := cli.ContainerList(opCtx, container.ListOptions{
			All: true,
			Filters: platform.ComposeProjectFilters(filters.KeyValuePair{
				Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", name),
			}),
		})
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		if len(containers) == 0 {
			platform.WriteError(w, "服务不存在", 404)
			return
		}
		switch action {
		case "start":
			err = cli.ContainerStart(opCtx, containers[0].ID, container.StartOptions{})
			if err == nil && (name == "xray" || name == "nginx") {
				proxy.RestoreServiceRotatingIP(opCtx, cli, containers[0].ID, name)
			}
		case "stop":
			t := platform.DockerStopTimeoutSeconds
			err = cli.ContainerStop(opCtx, containers[0].ID, container.StopOptions{Timeout: &t})
		case "restart":
			t := platform.DockerStopTimeoutSeconds
			err = cli.ContainerRestart(opCtx, containers[0].ID, container.StopOptions{Timeout: &t})
			// 始终尝试恢复轮换 IP，不受 restart 错误影响。
			if name == "xray" || name == "nginx" {
				proxy.RestoreServiceRotatingIP(opCtx, cli, containers[0].ID, name)
			}
		default:
			platform.WriteError(w, "未知操作", 400)
			return
		}
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] service action service=%s action=%s by=%s", name, action, actor)
		svcs := listServicesJSON(r.Context(), cli)
		platform.WriteJSON(w, map[string]any{"services": svcs})
	}
}

func ApiConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := map[string]any{
			"xrayInbounds": 0, "xrayClients": 0,
			"configStatus": "-",
		}
		var raw []byte
		// 单路径直读(此前是单元素循环的"多路径回退"残骸)。
		if fileData, err := os.ReadFile(proxy.XrayConfigPath); err == nil {
			raw = fileData
		}
		if len(raw) > 0 {
			var cfg struct {
				Inbounds []struct {
					Tag      string `json:"tag"`
					Settings struct {
						Clients []map[string]any `json:"clients"`
					} `json:"settings"`
				} `json:"inbounds"`
			}
			if json.Unmarshal(raw, &cfg) == nil {
				for _, ib := range cfg.Inbounds {
					if ib.Tag == "xhttp-in" {
						result["xrayClients"] = len(ib.Settings.Clients)
					}
				}
				result["xrayInbounds"] = len(cfg.Inbounds)
			}
			result["configStatus"] = "configured"
		}
		platform.WriteJSON(w, result)
	}
}

type svcJSON struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// Health 是 docker healthcheck 状态(healthy/starting/unhealthy),running
	// 但 healthcheck 连续失败的容器靠它才不会在面板上显示为绿色。
	Health string `json:"health,omitempty"`
	Image  string `json:"image"`
	Ports  string `json:"ports"`
}

func serviceSortWeight(name string) int {
	switch name {
	case "xray":
		return 0
	case "nginx":
		return 1
	case "media_server":
		return 2
	case "vod_extractor":
		return 3
	case "admin":
		return 4
	default:
		return 100
	}
}

func listServicesJSON(ctx context.Context, cli *client.Client) []svcJSON {
	// 读路径整体 deadline:此前只挂 r.Context()(客户端不断开就不取消),
	// docker daemon 挂起(socket 通但不响应)时 dashboard/services 页面无限
	// 转圈、goroutine 随并发缓慢堆积。10s 覆盖 list + 逐容器 inspect。
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: platform.ComposeProjectFilters(),
	})
	if err != nil {
		// 前端会拿到空列表;无日志的话 daemon 故障无法与"没有服务"区分。
		log.Printf("list services: %v", err)
		return nil
	}
	var services []svcJSON
	for _, c := range containers {
		var name string
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		if svc := c.Labels["com.docker.compose.service"]; svc != "" {
			name = svc
		}
		var ports []string
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				ports = append(ports, fmt.Sprintf("%d→%d", p.PublicPort, p.PrivatePort))
			} else {
				ports = append(ports, fmt.Sprintf("%d/%s", p.PrivatePort, p.Type))
			}
		}
		// Health 只在 inspect 里有(ContainerList 不带);仅对 running 容器
		// 查,inspect 失败静默留空(health 缺失时前端按无 healthcheck 展示)。
		health := ""
		if c.State == "running" {
			if info, ierr := cli.ContainerInspect(ctx, c.ID); ierr == nil &&
				info.State != nil && info.State.Health != nil {
				health = info.State.Health.Status
			}
		}
		services = append(services, svcJSON{
			Name: name, State: c.State, Health: health, Image: c.Image,
			Ports: strings.Join(ports, ", "),
		})
	}
	sort.Slice(services, func(i, j int) bool {
		wi, wj := serviceSortWeight(services[i].Name), serviceSortWeight(services[j].Name)
		if wi != wj {
			return wi < wj
		}
		return services[i].Name < services[j].Name
	})
	return services
}

var validServiceName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
