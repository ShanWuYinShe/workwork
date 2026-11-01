package proxy

import (
	"admin/internal/platform"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/docker/docker/client"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

func ApiNetworkStatus(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 整体收敛 deadline：此 handler 串行发起多轮 CF API + docker exec，
		// 全部挂在 r.Context() 上（客户端不断开就不取消），CF 慢时页面可挂起
		// 数分钟。20s 足够正常路径完成，异常时快速失败。
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		cfOK := platform.GetEnv("CF_API_TOKEN") != ""
		subnet := platform.GetEnv("IPV6_SUBNET")
		nginxStatic := platform.GetEnv("NGINX_IPV6")
		xrayStatic := platform.GetEnv("XRAY_IPV6")
		dnsXrayIP, dnsNginxIP, dnsRecords := LoadCurrentIPsWithRecords(ctx)
		xrayIP, nxIP := dnsXrayIP, dnsNginxIP
		if xrayID, err := platform.FindContainer(r.Context(), cli, "xray"); err == nil {
			xrayIP = selectContainerDNSIP(r.Context(), cli, xrayID, xrayStatic, dnsXrayIP)
		}
		if nginxID, err := platform.FindContainer(r.Context(), cli, "nginx"); err == nil {
			nxIP = selectContainerDNSIP(r.Context(), cli, nginxID, nginxStatic, dnsNginxIP)
		}

		// Derive allocation ranges from subnet's first /64
		subnetDisplay := ""
		prefix := ""
		staticRange := ""
		rotatingRange := ""
		if p, err := netip.ParsePrefix(subnet); err == nil && p.Addr().Is6() && !p.Addr().Is4In6() {
			p = p.Masked()
			ip := p.Addr().As16()
			parts := make([]string, 4)
			for i := 0; i < 4; i++ {
				parts[i] = fmt.Sprintf("%x", binary.BigEndian.Uint16(ip[i*2:i*2+2]))
			}
			prefix = strings.Join(parts, ":") + ":"
			subnetDisplay = strings.TrimSuffix(prefix, ":") + "::/64"
			staticRange = prefix + "1::/80"
			rotatingRange = prefix + "f::/80"
		}

		// CF DNS records for all domains
		type dnsInfo struct {
			Domain     string       `json:"domain"`
			Container  string       `json:"container"`
			ExpectedIP string       `json:"expected_ip"`
			Record     *cfDNSRecord `json:"record"`
			Error      string       `json:"error,omitempty"`
			Synced     bool         `json:"synced"`
		}
		var dnsResults []dnsInfo
		if cfOK {
			for _, di := range allDomainInfos() {
				expectedIP := nxIP
				if di.Container == "xray" {
					expectedIP = xrayIP
				}
				info := dnsInfo{Domain: di.Domain, Container: di.Container, ExpectedIP: expectedIP}
				// 复用 LoadCurrentIPsWithRecords 本轮已查到的记录：不再对同一
				// 批域名发起第二轮串行 CF 查询。查不到 = 记录缺失（rec=nil，
				// 展示为 mismatch），仅当本轮完全没查过（map 无键且非失败缓存）
				// 时才兜底补查。
				rec := dnsRecords[di.Domain]
				info.Record = rec
				if rec != nil {
					// 两端都归一化后再比较：CF 返回的 IPv6 压缩形式可能与
					// 预期值不同，直接字符串比较会误报 mismatch。
					info.Synced = platform.NormalizeIPv6(rec.Content) == platform.NormalizeIPv6(expectedIP)
				}
				dnsResults = append(dnsResults, info)
			}
		}

		platform.WriteJSON(w, map[string]any{
			"admin_port":     platform.AdminPort(),
			"subnet":         subnetDisplay,
			"static_range":   staticRange,
			"rotating_range": rotatingRange,
			"nginx_ip":       nginxStatic,
			"xray_static":    xrayStatic,
			"xray_ip":        xrayIP,
			"nginx_rotating": nxIP,
			"cf_configured":  cfOK,
			"dns_records":    dnsResults,
		})
	}
}
