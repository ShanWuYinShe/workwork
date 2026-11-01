package proxy

import (
	"admin/internal/platform"
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// XrayRestarter 实现 xray.Restarter，把容器重启能力注入 xray 域，
// 避免 accounts/xray 反向依赖 proxy 的网络实现。
type XrayRestarter struct {
	cli *client.Client
}

func NewXrayRestarter(cli *client.Client) *XrayRestarter {
	return &XrayRestarter{cli: cli}
}

func (r *XrayRestarter) RestartXray(ctx context.Context) error {
	return RestartXray(ctx, r.cli)
}

// restartXraySem 串行化 xray 容器重启。并发的客户端增删/用户删除各自
// GenXrayConfig + RestartXray；不串行化会出现两个 ContainerRestart 交错，
// 后一次重启可能抢在另一次配置渲染前完成。
var restartXraySem = make(chan struct{}, 1)

func RestartXray(ctx context.Context, cli *client.Client) error {
	// 等锁也受调用方 ctx 约束：排队超时直接返回，不再叠加一次重启。
	select {
	case restartXraySem <- struct{}{}:
		defer func() { <-restartXraySem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	id, err := platform.FindContainer(ctx, cli, "xray")
	if err != nil {
		if errors.Is(err, platform.ErrContainerNotFound) {
			return nil
		}
		return fmt.Errorf("find xray container: %w", err)
	}

	t := platform.DockerStopTimeoutSeconds
	restartErr := cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &t})

	// 始终尝试恢复轮换 IP。Docker restart 超时/cancel 时容器可能已完成重启，
	// 不恢复会导致 DNS 指向不存在的地址。恢复必须用独立 ctx:补偿针对的正是
	// restart 超时/取消场景,复用已死的 ctx 会让 CF 查询与 IP 补挂立即失败,
	// 补偿退化为一条误导性 WARNING(还因回退 env 报错静态 IP)。
	restoreCtx, restoreCancel := context.WithTimeout(context.WithoutCancel(ctx), platform.RestartTimeout)
	defer restoreCancel()
	RestoreServiceRotatingIP(restoreCtx, cli, id, "xray")

	if restartErr != nil {
		return fmt.Errorf("restart xray: %w", restartErr)
	}
	return nil
}

func RestoreServiceRotatingIP(ctx context.Context, cli *client.Client, containerID, service string) {
	if platform.GetEnv("CF_API_TOKEN") == "" {
		return
	}
	// 非阻塞获取读锁:启动轮换/手动轮换持写锁期间跳过本轮恢复(与
	// checkIPConsistency 的 TryRLock 一致),避免请求挂起在锁上。
	if !ipv6RotateMu.TryRLock() {
		return
	}
	defer ipv6RotateMu.RUnlock()
	xrayIP, nginxIP := LoadCurrentIPs(ctx)
	ip := xrayIP
	if service == "nginx" {
		ip = nginxIP
	}
	if ip == "" {
		return
	}
	if err := platform.AddContainerIP(ctx, cli, containerID, ip); err != nil {
		log.Printf("WARNING: failed to restore %s rotating IPv6 %s: %v", service, ip, err)
	}
}
