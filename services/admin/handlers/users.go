package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

func ReadXrayClients() ([]map[string]string, error) {
	data, err := os.ReadFile("/app/data/xray-clients.json")
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
	RealityDomain     string
	RealityServerName string
	RealityPublicKey  string
	RealityShortID    string
	XHTTPDomain       string
	XHTTPProxyAuth    string // "user:pass" for nginx basic auth
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
		"tcpcongestion":        "bbr",
	}
}

func xhttpOutboundSockopt() map[string]any {
	sockopt := baseProxySockopt()
	sockopt["domainStrategy"] = "UseIP"
	sockopt["happyEyeballs"] = map[string]any{
		"tryDelayMs":       250,
		"prioritizeIPv6":   true,
		"interleave":       1,
		"maxConcurrentTry": 4,
	}
	return sockopt
}

func realityOutboundSockopt() map[string]any {
	return xhttpOutboundSockopt()
}

func autoProxyDomains() []string {
	return []string{
		"geosite:google", "geosite:github", "geosite:openai",
		"geosite:docker", "geosite:netflix", "geosite:instagram",
		"geosite:youtube", "geosite:facebook", "geosite:twitter",
	}
}

func autoProxyPingConfig() map[string]any {
	return map[string]any{
		"destination":  "https://www.gstatic.com/generate_204",
		"connectivity": "https://cp.cloudflare.com/cdn-cgi/trace",
		"interval":     "30s",
		"sampling":     10,
		"timeout":      "5s",
		"httpMethod":   "HEAD",
	}
}

func GenerateClientConfig(clients []map[string]string, cfg ClientConfig) string {
	uuid := "YOUR_UUID"
	if len(clients) > 0 {
		uuid = clients[0]["id"]
	}
	if cfg.RealityDomain == "" || cfg.RealityPublicKey == "" || cfg.XHTTPDomain == "" {
		return "{\"error\": \"Server configuration incomplete\"}"
	}
	return orderedJSON([]jsonField{
		{"log", map[string]any{"loglevel": "warning"}},
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
				"tag": "reality", "protocol": "vless",
				"settings": map[string]any{
					"vnext": []any{map[string]any{
						"address": cfg.RealityDomain, "port": 443,
						"users": []any{map[string]any{
							"id": uuid, "email": uuid, "flow": "xtls-rprx-vision", "encryption": "none",
						}},
					}},
				},
				"streamSettings": map[string]any{
					"network": "tcp", "security": "reality",
					"realitySettings": map[string]any{
						"serverName": cfg.RealityServerName, "fingerprint": "chrome",
						"publicKey": cfg.RealityPublicKey, "shortId": cfg.RealityShortID,
					},
					"sockopt": realityOutboundSockopt(),
				},
			},
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
			"domainStrategy": "IPIfNonMatch",
			"balancers": []any{map[string]any{
				"tag":      "proxy",
				"selector": []string{"reality", "xhttp"},
				"strategy": map[string]any{"type": "leastPing"},
			}},
			"rules": []any{
				map[string]any{"type": "field", "outboundTag": "reject", "domain": []string{"geosite:category-ads-all"}},
				map[string]any{"type": "field", "balancerTag": "proxy", "domain": autoProxyDomains()},
				map[string]any{"type": "field", "outboundTag": "direct", "network": "tcp,udp"},
			},
		}},
		{"burstObservatory", map[string]any{
			"subjectSelector": []string{"reality", "xhttp"},
			"pingConfig":      autoProxyPingConfig(),
		}},
	})
}

func xhttpSettings(cfg ClientConfig) map[string]any {
	s := map[string]any{"path": "/proxy/query", "mode": "stream-up"}
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
