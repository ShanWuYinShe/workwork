package main

import (
	"context"
	"log"
	"time"

	"github.com/docker/docker/client"
)

const ipConsistencyInterval = 3 * time.Minute

// ipConsistencyChecker 定期验证 xray/nginx 容器是否持有 DNS 中记录的轮换 IPv6 地址。
// 如果缺失（如 admin 宕机期间容器被重启、Docker daemon 重启导致容器重建等），
// 自动补上。这是事件监听器之外的兜底机制。
func ipConsistencyChecker(ctx context.Context, cli *client.Client, startupDone <-chan struct{}) {
	if getEnv("CF_API_TOKEN") == "" {
		return
	}

	// 等待 startupRotateIPv6 完成，避免与启动轮换竞态：
	// startup 会先清理旧 IP 再分配新 IP，如果一致性检查在中间插入，
	// 会把 DNS 中的旧 IP 加回去，导致旧 IP 残留。
	select {
	case <-ctx.Done():
		return
	case <-startupDone:
	}

	ticker := time.NewTicker(ipConsistencyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkIPConsistency(ctx, cli)
		}
	}
}

func checkIPConsistency(ctx context.Context, cli *client.Client) {
	// 避免与正在进行的轮换操作冲突；获取不到锁就跳过本轮
	if !ipv6RotateMu.TryLock() {
		return
	}
	defer ipv6RotateMu.Unlock()

	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	xrayIP, nginxIP := loadCurrentIPs()

	for _, svc := range []struct {
		name   string
		wantIP string
	}{
		{"xray", xrayIP},
		{"nginx", nginxIP},
	} {
		if svc.wantIP == "" {
			continue
		}

		id, err := findContainer(checkCtx, cli, svc.name)
		if err != nil {
			log.Printf("[ip-consistency] %s container not found: %v", svc.name, err)
			continue
		}

		assigned := getContainerIPs(checkCtx, cli, id)

		found := false
		for _, ip := range assigned {
			if ip == svc.wantIP {
				found = true
				break
			}
		}

		if !found {
			log.Printf("[ip-consistency] %s missing rotating IP %s (has %v), restoring", svc.name, svc.wantIP, assigned)
			if err := addContainerIP(checkCtx, cli, id, svc.wantIP); err != nil {
				log.Printf("[ip-consistency] failed to restore %s IP: %v", svc.name, err)
			}
		}
	}
}
