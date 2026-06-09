package main

import (
	"context"
	"log"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// ipWatcher 订阅 Docker 容器 start 事件，当 xray 或 nginx 容器启动时
// 自动恢复其轮换 IPv6 地址。覆盖所有容器生命周期事件，包括：
//   - 宿主机 docker compose 操作
//   - Docker restart policy（主机重启后）
//   - 手动 docker restart 命令
//   - 其他任何导致容器网络命名空间重建的操作
func ipWatcher(ctx context.Context, cli *client.Client) {
	if getEnv("CF_API_TOKEN") == "" {
		return
	}

	backoff := 2 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		// 只订阅容器 start 事件（restart 本身也会产生 start 事件）
		eventFilters := filters.NewArgs(
			filters.KeyValuePair{Key: "type", Value: "container"},
			filters.KeyValuePair{Key: "event", Value: "start"},
		)

		msgCh, errCh := cli.Events(ctx, events.ListOptions{Filters: eventFilters})

		for {
			select {
			case <-ctx.Done():
				return
			case err := <-errCh:
				log.Printf("[ip-watcher] event stream error: %v; reconnecting in %v", err, backoff)
				time.Sleep(backoff)
				backoff = min(backoff*2, maxBackoff)
				goto reconnect
			case msg := <-msgCh:
				service := msg.Actor.Attributes["com.docker.compose.service"]
				if service != "xray" && service != "nginx" {
					continue
				}
				containerID := msg.Actor.ID

				log.Printf("[ip-watcher] detected %s start, restoring rotating IP", service)

				// 等待容器网络命名空间初始化完成
				time.Sleep(2 * time.Second)

				restoreServiceRotatingIP(ctx, cli, containerID, service)

				// 成功处理事件后重置退避
				backoff = 2 * time.Second
			}
		}

	reconnect:
	}
}
