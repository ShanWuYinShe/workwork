package platform

import (
	"net/netip"
	"strings"
)

func NormalizeIPv6(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is6() || addr.Is4In6() {
		// 解析失败返回空:让脏输入(如被篡改的 DNS 记录内容)止步于此,
		// 而不是原样透传进状态文件/容器 exec 参数。
		return ""
	}
	return addr.String()
}

func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
