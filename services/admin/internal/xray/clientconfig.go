package xray

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

type ClientConfig struct {
	XHTTPDomain string
	// XHTTPAddress 是客户端出站连接地址(优选入口域名或 IP),留空则跟随
	// XHTTPDomain。TLS SNI/证书名恒取 XHTTPDomain——优选入口是 Cloudflare
	// DNS-only 记录,其域名没有边缘证书,当 SNI 用会握手失败。
	XHTTPAddress string
	// XHTTPALPN 是客户端与 Cloudflare 边缘协商的应用层协议:"h2"(默认,
	// TCP,各网络环境稳妥)或 "h3"(QUIC/UDP,UDP 443 质量好的网络更快)。
	// h3 走 QUIC,TCP 专属 sockopt(TFO/keepalive/userTimeout/bbr)对其
	// 不生效,生成时直接剔除。
	XHTTPALPN      string
	XHTTPProxyAuth string // "user:pass" for nginx basic auth
}

// Ordered JSON builder: preserves field order in output.
type jsonField struct {
	key string
	val any
}

func baseProxySockopt() map[string]any {
	return map[string]any{
		"tcpFastOpen":          true,
		"tcpKeepAliveIdle":     45,
		"tcpKeepAliveInterval": 45,
		"tcpUserTimeout":       10000,
		"tcpCongestion":        "bbr",
	}
}

func xhttpOutboundSockopt() map[string]any {
	sockopt := baseProxySockopt()
	sockopt["domainStrategy"] = "UseIP"
	sockopt["happyEyeballs"] = map[string]any{
		"tryDelayMs":       1,
		"prioritizeIPv6":   false,
		"interleave":       1,
		"maxConcurrentTry": 4,
	}
	return sockopt
}

func autoProxyDomains() []string {
	return []string{
		"geosite:google", "geosite:github", "geosite:openai",
		"geosite:docker", "geosite:netflix", "geosite:cloudflare",
		"geosite:instagram", "geosite:youtube", "geosite:facebook",
		"geosite:twitter", "geosite:figma", "geosite:npmjs",
		"geosite:mozilla",
		"domain:antigravity-unleash.goog",
		"domain:opencode.ai",
		"domain:investing.com",
		"domain:textnow.com",
		"domain:toutoupa.com",
	}
}

func GenerateClientConfig(clients []Client, cfg ClientConfig) string {
	uuid := "YOUR_UUID"
	if len(clients) > 0 {
		uuid = clients[0].ID
	}
	if cfg.XHTTPDomain == "" {
		return "{\"error\": \"Server configuration incomplete\"}"
	}
	return orderedJSON([]jsonField{
		{"log", map[string]any{"loglevel": "warning"}},
		// 客户端内置 DNS：sockopt domainStrategy=UseIP 依赖它解析代理服务
		// 器域名(含 XHTTP_ADDRESS 指向的优选入口)。并行查询取最快,加密支路
		// 防污染,两条国内可达的 DoH 互为备份——境外 DoH(如 1.1.1.1)国内直连
		// 超时,每轮解析都刷 [Error] 日志且永远不中奖。1.12.12.12 是 DNSPod
		// 的 DoH 入口(证书含该 IP SAN,可直接作 DOHL 目标),223.5.5.5 是阿里
		// DoH。明文 UDP 支路以国内递归为主(223.5.5.5/119.29.29.29),保留
		// 8.8.8.8 作境外兜底。
		{"dns", map[string]any{
			"servers": []string{
				"https+local://223.5.5.5/dns-query",
				"https+local://1.12.12.12/dns-query",
				"223.5.5.5",
				"119.29.29.29",
				"8.8.8.8",
			},
			"queryStrategy":       "UseIP",
			"enableParallelQuery": true,
			"useSystemHosts":      true,
		}},
		{"inbounds", []any{
			map[string]any{
				"tag": "socks", "port": 10808, "listen": "127.0.0.1", "protocol": "socks",
				"settings": map[string]any{"udp": true},
				"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
			},
			map[string]any{
				"tag": "http", "port": 10809, "listen": "127.0.0.1", "protocol": "http",
				"settings": map[string]any{},
				"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
			},
		}},
		{"outbounds", []any{
			map[string]any{
				"tag": "xhttp", "protocol": "vless",
				"settings": map[string]any{
					"vnext": []any{map[string]any{
						"address": clientAddress(cfg), "port": 443,
						"users": []any{map[string]any{
							"id": uuid, "email": uuid, "encryption": "none",
						}},
					}},
				},
				"streamSettings": map[string]any{
					"network": "xhttp", "security": "tls",
					"tlsSettings":   map[string]any{"serverName": cfg.XHTTPDomain, "fingerprint": "chrome", "alpn": clientALPN(cfg)},
					"xhttpSettings": xhttpSettings(cfg),
					"sockopt":       clientSockopt(cfg),
				},
			},
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "reject", "protocol": "blackhole"},
		}},
		{"routing", map[string]any{
			"domainStrategy": "AsIs",
			"rules": []any{
				map[string]any{"type": "field", "outboundTag": "reject", "domain": []string{"geosite:category-ads-all"}},
				map[string]any{"type": "field", "outboundTag": "xhttp", "domain": autoProxyDomains()},
				map[string]any{"type": "field", "outboundTag": "direct", "network": "tcp,udp"},
			},
		}},
	})
}

// clientAddress 返回客户端出站连接地址:XHTTP_ADDRESS(优选入口)优先,留空
// 回落到 XHTTP_DOMAIN。两者语义不同——XHTTP_DOMAIN 同时是 TLS SNI/证书名、
// nginx server_name 与 CF DNS 同步锚点,不能被优选域名覆盖。
func clientAddress(cfg ClientConfig) string {
	if addr := strings.TrimSpace(cfg.XHTTPAddress); addr != "" {
		return addr
	}
	return cfg.XHTTPDomain
}

// clientALPN 返回生成的 ALPN 列表:XHTTP_ALPN 取 "h2"(默认)或 "h3"。
func clientALPN(cfg ClientConfig) []string {
	if strings.TrimSpace(cfg.XHTTPALPN) == "h3" {
		return []string{"h3"}
	}
	return []string{"h2"}
}

// clientSockopt 按 ALPN 生成出站 sockopt:两条路径都靠 UseIP + happyEyeballs
// 解析并拨号代理域名;h3 走 QUIC,TCP 专属选项(TFO/keepalive/userTimeout/bbr)
// 对它不生效,保留只会让配置携带死参数——h2 则保留经线上验证的 TCP 调优。
func clientSockopt(cfg ClientConfig) map[string]any {
	if clientALPN(cfg)[0] == "h3" {
		return map[string]any{
			"domainStrategy": "UseIP",
			"happyEyeballs": map[string]any{
				"tryDelayMs":       1,
				"prioritizeIPv6":   false,
				"interleave":       1,
				"maxConcurrentTry": 4,
			},
		}
	}
	return xhttpOutboundSockopt()
}

func xhttpSettings(cfg ClientConfig) map[string]any {
	s := map[string]any{"path": "/proxy/query", "mode": "packet-up"}
	if cfg.XHTTPProxyAuth != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(cfg.XHTTPProxyAuth))
		s["headers"] = map[string]any{"Authorization": "Basic " + auth}
	}
	return s
}

func orderedJSON(fields []jsonField) string {
	var buf []byte
	buf = append(buf, "{\n"...)
	for i, f := range fields {
		val, _ := json.MarshalIndent(f.val, "  ", "  ")
		buf = append(buf, fmt.Sprintf("  %q: %s", f.key, val)...)
		if i < len(fields)-1 {
			buf = append(buf, ',')
		}
		buf = append(buf, '\n')
	}
	buf = append(buf, "}\n"...)
	return string(buf)
}
