package proxy

import (
	"context"

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
