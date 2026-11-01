package proxy

import (
	"admin/internal/platform"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const rotatingIPv6CleanupGrace = 10 * time.Minute

var ipv6RotateMu sync.RWMutex

// -- Network / IPv6 rotation APIs --

// LoadCurrentIPs 是 LoadCurrentIPsWithRecords 的兼容包装（不需要记录明细的
// 调用方继续用两返回值签名）。
func LoadCurrentIPs(ctx context.Context) (xrayIP, nginxIP string) {
	x, n, _ := LoadCurrentIPsWithRecords(ctx)
	return x, n
}

// LoadCurrentIPsWithRecords 除返回各容器当前 IPv6 外，还带出本次轮询查到的
// AAAA 记录明细，供 ApiNetworkStatus 的 DNS 表格复用——同一请求内不再对
// 同一批域名发起第二轮全量 CF 查询（此前单次页面刷新 = 域数 × 2 轮串行 CF API）。
func LoadCurrentIPsWithRecords(ctx context.Context) (xrayIP, nginxIP string, records map[string]*cfDNSRecord) {
	// env 回退值过 platform.NormalizeIPv6：CF 路径与 platform.GetContainerIPs 返回的都是压缩
	// 规范形，env 若填了全展开形式，ip_consistency 的字符串比较会永假，
	// 每 3 分钟误报一次 "missing rotating IP"（日志噪音 + no-op add）。
	xrayIP = platform.NormalizeIPv6(platform.GetEnv("XRAY_IPV6"))
	nginxIP = platform.NormalizeIPv6(platform.GetEnv("NGINX_IPV6"))
	records = map[string]*cfDNSRecord{}
	if platform.GetEnv("CF_API_TOKEN") == "" {
		return
	}
	for _, di := range allDomainInfos() {
		rec, err := cfFindAAAARecord(ctx, di.Domain)
		if err != nil || rec == nil {
			continue
		}
		records[di.Domain] = rec
		if di.Container == "xray" {
			xrayIP = platform.NormalizeIPv6(rec.Content)
		} else {
			nginxIP = platform.NormalizeIPv6(rec.Content)
		}
	}
	return
}

func parseSubnetPrefix(subnet string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return "", fmt.Errorf("parse subnet: %w", err)
	}
	prefix = prefix.Masked()
	// Return the first 64 bits as hex string with trailing ::
	addr := prefix.Addr()
	if !addr.Is6() || addr.Is4In6() {
		return "", fmt.Errorf("not an IPv6 subnet")
	}
	// Build prefix like "2605:a141:2322:2623:"
	ip := addr.As16()
	parts := make([]string, 4)
	for i := 0; i < 4; i++ {
		parts[i] = fmt.Sprintf("%x", binary.BigEndian.Uint16(ip[i*2:i*2+2]))
	}
	return strings.Join(parts, ":") + ":", nil
}

func genRandomIPv6(prefix string, exclude ...string) (string, error) {
	excludeSet := map[string]bool{}
	for _, e := range exclude {
		if normalized := platform.NormalizeIPv6(e); normalized != "" {
			excludeSet[normalized] = true
		}
	}
	for attempts := 0; attempts < 100; attempts++ {
		var ifid [8]byte
		if _, err := io.ReadFull(rand.Reader, ifid[:]); err != nil {
			return "", err
		}
		// 5th hextet fixed to 000f → all rotating IPs live in :f::/80,
		// never colliding with static IPs in :1::/80 range.
		ip := fmt.Sprintf("%s%x:%x:%x:%x",
			prefix,
			0xf,
			binary.BigEndian.Uint16(ifid[0:2]),
			binary.BigEndian.Uint16(ifid[2:4]),
			binary.BigEndian.Uint16(ifid[4:6]),
		)
		ip = platform.NormalizeIPv6(ip)
		if !excludeSet[ip] {
			return ip, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique IPv6 after 100 attempts")
}
