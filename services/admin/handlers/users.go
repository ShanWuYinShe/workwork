package handlers

import (
	"encoding/json"
	"fmt"
	"os"
)

func ReadXrayClients() ([]map[string]string, error) {
	data, err := os.ReadFile("/app/configs/xray.json")
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Settings struct {
				Clients []map[string]string `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var clients []map[string]string
	for _, ib := range cfg.Inbounds {
		if ib.Tag == "reality-in" || ib.Tag == "xhttp-in" {
			for _, c := range ib.Settings.Clients {
				id := c["id"]
				if !seen[id] {
					seen[id] = true
					clients = append(clients, c)
				}
			}
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
}

// Ordered JSON builder: preserves field order in output.
type jsonField struct {
	key string
	val any
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

func GenerateClientConfig(clients []map[string]string, cfg ClientConfig) string {
	uuid := "YOUR_UUID"
	if len(clients) > 0 {
		uuid = clients[0]["id"]
	}

	realityAddr := cfg.RealityDomain
	if realityAddr == "" {
		realityAddr = "xray.yinghuoren.com"
	}
	sni := cfg.RealityServerName
	if sni == "" {
		sni = "www.microsoft.com"
	}
	pubKey := cfg.RealityPublicKey
	if pubKey == "" {
		pubKey = "FCgB02VAjngf517UBQ-LWUWh6ZrvdHMWVUmHHkyirhM"
	}
	shortID := cfg.RealityShortID
	if shortID == "" {
		shortID = "abcdef"
	}
	domain := cfg.XHTTPDomain
	if domain == "" {
		domain = "yinghuoren.com"
	}

	return orderedJSON([]jsonField{
		{"log", map[string]any{"loglevel": "warning"}},
		{"inbounds", []any{
			map[string]any{
				"tag": "socks", "port": 10808, "listen": "127.0.0.1", "protocol": "socks",
				"settings": map[string]any{"udp": true},
				"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}},
			},
			map[string]any{
				"tag": "http", "port": 10809, "listen": "127.0.0.1", "protocol": "http",
				"settings": map[string]any{},
				"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}},
			},
		}},
		{"outbounds", []any{
			map[string]any{
				"tag": "reality", "protocol": "vless",
				"settings": map[string]any{
					"vnext": []any{map[string]any{
						"address": realityAddr, "port": 443,
						"users": []any{map[string]any{
							"id": uuid, "email": uuid, "flow": "xtls-rprx-vision", "encryption": "none",
						}},
					}},
				},
				"streamSettings": map[string]any{
					"network": "tcp", "security": "reality",
					"realitySettings": map[string]any{
						"serverName": sni, "fingerprint": "chrome",
						"publicKey": pubKey, "shortId": shortID,
					},
					"sockopt": map[string]any{"tcpFastOpen": true, "tcpCongestion": "bbr", "tcpKeepAliveInterval": 30},
				},
			},
			map[string]any{
				"tag": "xhttp", "protocol": "vless",
				"settings": map[string]any{
					"vnext": []any{map[string]any{
						"address": domain, "port": 443,
						"users": []any{map[string]any{
							"id": uuid, "email": uuid, "encryption": "none",
						}},
					}},
				},
				"streamSettings": map[string]any{
					"network": "xhttp", "security": "tls",
					"tlsSettings":   map[string]any{"serverName": domain, "fingerprint": "chrome"},
					"xhttpSettings": map[string]any{"path": "/proxy/query", "mode": "auto"},
					"sockopt":       map[string]any{"tcpFastOpen": true, "tcpCongestion": "bbr", "tcpKeepAliveInterval": 30},
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
				map[string]any{"type": "field", "balancerTag": "proxy", "domain": []string{
					"geosite:google", "geosite:github", "geosite:openai",
					"geosite:docker", "geosite:netflix", "geosite:instagram",
					"geosite:youtube", "geosite:facebook", "geosite:twitter",
				}},
				map[string]any{"type": "field", "outboundTag": "direct", "network": "tcp,udp"},
			},
		}},
		{"burstObservatory", map[string]any{
			"subjectSelector": []string{"reality", "xhttp"},
			"pingConfig": map[string]any{
				"destination":  "https://cp.cloudflare.com/generate_204",
				"connectivity": "https://www.google.com/generate_204",
				"interval":     "30s",
				"sampling":     10,
				"timeout":      "5s",
				"httpMethod":   "HEAD",
			},
		}},
	})
}
