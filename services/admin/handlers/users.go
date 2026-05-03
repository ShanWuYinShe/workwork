package handlers

import (
	"encoding/json"
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

func GenerateClientConfig(clients []map[string]string) string {
	uuid := "YOUR_UUID"
	if len(clients) > 0 {
		uuid = clients[0]["id"]
	}

	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []map[string]any{
			{
				"tag":      "socks",
				"port":     10808,
				"listen":   "127.0.0.1",
				"protocol": "socks",
				"settings": map[string]any{"udp": true},
				"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}},
			},
			{
				"tag":      "http",
				"port":     10809,
				"listen":   "127.0.0.1",
				"protocol": "http",
				"settings": map[string]any{},
				"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}},
			},
		},
		"outbounds": []map[string]any{
			{
				"tag":      "reality",
				"protocol": "vless",
				"settings": map[string]any{
					"vnext": []map[string]any{{"address": "2605:a141:2322:2623:1::12", "port": 443, "users": []map[string]any{{"id": uuid, "flow": "xtls-rprx-vision", "encryption": "none"}}}},
				},
				"streamSettings": map[string]any{
					"network": "tcp", "security": "reality",
					"realitySettings": map[string]any{
						"serverName": "www.microsoft.com", "fingerprint": "chrome",
						"publicKey": "FCgB02VAjngf517UBQ-LWUWh6ZrvdHMWVUmHHkyirhM", "shortId": "abcdef",
					},
					"sockopt": map[string]any{"tcpFastOpen": true, "tcpCongestion": "bbr", "tcpKeepAliveInterval": 30},
				},
			},
			{
				"tag":      "xhttp",
				"protocol": "vless",
				"settings": map[string]any{
					"vnext": []map[string]any{{"address": "yinghuoren.com", "port": 443, "users": []map[string]any{{"id": uuid, "encryption": "none"}}}},
				},
				"streamSettings": map[string]any{
					"network": "xhttp", "security": "tls",
					"tlsSettings": map[string]any{"serverName": "yinghuoren.com", "fingerprint": "chrome"},
					"xhttpSettings": map[string]any{"path": "/proxy/query", "mode": "auto"},
					"sockopt": map[string]any{"tcpFastOpen": true, "tcpCongestion": "bbr", "tcpKeepAliveInterval": 30},
				},
			},
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "reject", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"domainStrategy": "IPIfNonMatch",
			"balancers": []map[string]any{{"tag": "proxy", "selector": []string{"reality", "xhttp"}, "strategy": map[string]any{"type": "random"}}},
			"rules": []map[string]any{
				{"type": "field", "outboundTag": "reject", "domain": []string{"geosite:category-ads-all"}},
				{"type": "field", "balancerTag": "proxy", "domain": []string{"geosite:google", "geosite:github", "geosite:openai", "geosite:docker", "geosite:netflix", "geosite:instagram", "geosite:youtube", "geosite:facebook", "geosite:twitter"}},
				{"type": "field", "outboundTag": "direct", "network": "tcp,udp"},
			},
		},
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return `{"error": "failed to generate config"}`
	}
	return string(out)
}
