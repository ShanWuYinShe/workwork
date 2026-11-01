package xray

import (
	"admin/internal/platform"
	"fmt"
	"strings"
)

// ValidateProxyConfigEnv 校验生成客户端配置所需的环境变量；启动期 fail-fast。
func ValidateProxyConfigEnv() error {
	if strings.TrimSpace(platform.GetEnv("XHTTP_DOMAIN")) == "" {
		return fmt.Errorf("missing required env var: XHTTP_DOMAIN")
	}
	// XHTTP_ADDRESS(可选)是客户端出站连接地址(优选入口)。只接受裸主机名或
	// IP:带 scheme/路径/空白的值会被原样写进生成配置的 vnext.address。
	if addr := strings.TrimSpace(platform.GetEnv("XHTTP_ADDRESS")); addr != "" {
		if strings.ContainsAny(addr, "/ \t") {
			return fmt.Errorf("XHTTP_ADDRESS must be a bare host or IP (no scheme/path/whitespace), got %q", addr)
		}
	}
	switch strings.ToLower(strings.TrimSpace(platform.GetEnv("XHTTP_ALPN"))) {
	case "", "h2", "h3":
	default:
		return fmt.Errorf("XHTTP_ALPN must be h2 or h3, got %q", platform.GetEnv("XHTTP_ALPN"))
	}
	return nil
}
