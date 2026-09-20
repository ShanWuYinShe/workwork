package accounts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

// readXrayClientMapsNoLock 读取 clients 并转成 map 形式(handler 展示用)。
// 命名含 NoLock:调用方必须已持有 xrayClientsMu(与 readXrayClientsNoLock
// 同一约定)。此前名为 ReadXrayClients(大写、无锁),与已删除的加锁版
// readXrayClients 仅大小写之差——曾有调用方误用加锁版造成双重加锁死锁的风险。
func readXrayClientMapsNoLock() ([]map[string]string, error) {
	// 走 xrayClientsPath 变量而非字面量:同包读写路径共用同一 var,测试可
	// 替换路径,未来改路径不会静默分叉。
	data, err := os.ReadFile(xrayClientsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []struct {
		ID    string `json:"id"`
		Owner string `json:"owner"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	clients := make([]map[string]string, len(entries))
	for i, e := range entries {
		clients[i] = map[string]string{
			"id":    e.ID,
			"owner": e.Owner,
			"email": e.Email,
		}
	}
	return clients, nil
}

type ClientConfig struct {
	XHTTPDomain    string
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

func GenerateClientConfig(clients []map[string]string, cfg ClientConfig) string {
	uuid := "YOUR_UUID"
	if len(clients) > 0 {
		uuid = clients[0]["id"]
	}
	if cfg.XHTTPDomain == "" {
		return "{\"error\": \"Server configuration incomplete\"}"
	}
	return orderedJSON([]jsonField{
		{"log", map[string]any{"loglevel": "warning"}},
		// 客户端内置 DNS：sockopt domainStrategy=UseIP 依赖它解析代理服务
		// 器域名。并行查询取最快，223.5.5.5 照顾国内解析，DoH 兜底。
		{"dns", map[string]any{
			"servers":             []string{"https+local://1.1.1.1/dns-query", "8.8.8.8", "223.5.5.5"},
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
						"address": cfg.XHTTPDomain, "port": 443,
						"users": []any{map[string]any{
							"id": uuid, "email": uuid, "encryption": "none",
						}},
					}},
				},
				"streamSettings": map[string]any{
					"network": "xhttp", "security": "tls",
					"tlsSettings":   map[string]any{"serverName": cfg.XHTTPDomain, "fingerprint": "chrome", "alpn": []string{"h2"}},
					"xhttpSettings": xhttpSettings(cfg),
					"sockopt":       xhttpOutboundSockopt(),
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
