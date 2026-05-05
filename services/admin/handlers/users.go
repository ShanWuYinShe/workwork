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

type ClientConfig struct {
	XrayIPV6           string
	RealityServerName  string
	RealityPublicKey   string
	RealityShortID     string
	XHTTPDomain        string
}

func GenerateClientConfig(clients []map[string]string, cfg ClientConfig) string {
	uuid := "YOUR_UUID"
	if len(clients) > 0 {
		uuid = clients[0]["id"]
	}

	ip := cfg.XrayIPV6
	if ip == "" {
		ip = "2605:a141:2322:2623:1::12"
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

	out := map[string]any{
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
					"vnext": []map[string]any{{"address": ip, "port": 443, "users": []map[string]any{{"id": uuid, "flow": "xtls-rprx-vision", "encryption": "none"}}}},
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
			{
				"tag":      "xhttp",
				"protocol": "vless",
				"settings": map[string]any{
					"vnext": []map[string]any{{"address": domain, "port": 443, "users": []map[string]any{{"id": uuid, "encryption": "none"}}}},
				},
				"streamSettings": map[string]any{
					"network": "xhttp", "security": "tls",
					"tlsSettings": map[string]any{"serverName": domain, "fingerprint": "chrome"},
					"xhttpSettings": map[string]any{"path": "/proxy/query", "mode": "auto"},
					"sockopt": map[string]any{"tcpFastOpen": true, "tcpCongestion": "bbr", "tcpKeepAliveInterval": 30},
				},
			},
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "reject", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"domainStrategy": "IPIfNonMatch",
			"balancers": []map[string]any{{"tag": "proxy", "selector": []string{"reality", "xhttp"}}},
			"rules": []map[string]any{
				{"type": "field", "outboundTag": "reject", "domain": []string{"geosite:category-ads-all"}},
				{"type": "field", "balancerTag": "proxy", "domain": []string{"geosite:google", "geosite:github", "geosite:openai", "geosite:docker", "geosite:netflix", "geosite:instagram", "geosite:youtube", "geosite:facebook", "geosite:twitter"}},
				{"type": "field", "outboundTag": "direct", "network": "tcp,udp"},
			},
		},
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return `{"error": "failed to generate config"}`
	}
	return string(b)
}
