package main

import (
	"admin/handlers"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateClientConfigXhttpOnly(t *testing.T) {
	cfgJSON := handlers.GenerateClientConfig([]map[string]string{{"id": "uuid-1"}}, handlers.ClientConfig{
		XHTTPDomain:    "example.com",
		XHTTPProxyAuth: "viewer:secret",
	})

	var doc map[string]any
	if err := json.Unmarshal([]byte(cfgJSON), &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	outbounds, ok := doc["outbounds"].([]any)
	if !ok || len(outbounds) != 3 {
		t.Fatalf("expected exactly xhttp/direct/reject outbounds, got %#v", doc["outbounds"])
	}

	// Reality 已暂时停用：不得再出现 reality 出站、balancer 与 observatory。
	for _, ob := range outbounds {
		if nestedMapFromAny(t, ob)["tag"] == "reality" {
			t.Fatal("reality outbound must be absent while reality is disabled")
		}
	}
	if _, ok := doc["observatory"]; ok {
		t.Fatal("observatory must be absent while reality is disabled")
	}

	xhttp := outboundByTag(t, outbounds, "xhttp")
	assertXHTTPSockopt(t, xhttp, "UseIP")

	xhttpSettings := nestedMap(t, nestedMap(t, xhttp, "streamSettings"), "xhttpSettings")
	headers := nestedMap(t, xhttpSettings, "headers")
	if headers["Authorization"] == "" {
		t.Fatal("expected xhttp auth header to be present")
	}

	routing := nestedMap(t, doc, "routing")
	if _, ok := routing["balancers"]; ok {
		t.Fatal("routing balancers must be absent while reality is disabled")
	}
	rules, ok := routing["rules"].([]any)
	if !ok || len(rules) < 2 {
		t.Fatalf("unexpected routing rules: %#v", routing["rules"])
	}
	proxyRule := nestedMapFromAny(t, rules[1])
	if proxyRule["outboundTag"] != "xhttp" {
		t.Fatalf("expected proxy rule to point at xhttp, got %#v", proxyRule["outboundTag"])
	}
	domains, ok := proxyRule["domain"].([]any)
	if !ok {
		t.Fatalf("unexpected proxy domains: %#v", proxyRule["domain"])
	}
	if !containsAny(domains, "geosite:youtube") {
		t.Fatalf("expected proxy domains to include geosite:youtube, got %#v", domains)
	}

	// XHTTP_DOMAIN 缺失即视为服务端配置不完整。
	if got := handlers.GenerateClientConfig(nil, handlers.ClientConfig{}); !strings.Contains(got, "incomplete") {
		t.Fatalf("expected incomplete-config error, got %s", got)
	}
}

func TestXrayTemplateRendersValidJSON(t *testing.T) {
	templatePath := filepath.Join("..", "xray", "config.json.tmpl")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}

	rendered := string(data)
	replacements := map[string]string{
		"__XRAY_CLIENTS_JSON__": `[{"id":"uuid-1","email":"viewer","flow":"","owner":"viewer"}]`,
	}
	for placeholder, value := range replacements {
		rendered = strings.ReplaceAll(rendered, placeholder, value)
	}
	if strings.Contains(rendered, "__XRAY_") {
		t.Fatal("rendered template still contains unreplaced __XRAY_* placeholders")
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("unmarshal rendered template: %v", err)
	}

	dns := nestedMap(t, doc, "dns")
	if dns["queryStrategy"] != "UseIP" {
		t.Fatalf("unexpected dns query strategy: %#v", dns["queryStrategy"])
	}
	outbounds, ok := doc["outbounds"].([]any)
	if !ok || len(outbounds) == 0 {
		t.Fatalf("unexpected outbounds: %#v", doc["outbounds"])
	}
	direct := outboundByTag(t, outbounds, "direct")
	streamSettings := nestedMap(t, direct, "streamSettings")
	sockopt := nestedMap(t, streamSettings, "sockopt")
	if sockopt["domainStrategy"] != "UseIP" {
		t.Fatalf("unexpected direct sockopt domainStrategy: %#v", sockopt["domainStrategy"])
	}
	blocked := outboundByTag(t, outbounds, "blocked")
	if blocked["protocol"] != "blackhole" {
		t.Fatalf("unexpected blocked outbound: %#v", blocked)
	}
	routing := nestedMap(t, doc, "routing")
	rules, ok := routing["rules"].([]any)
	if !ok || len(rules) == 0 {
		t.Fatalf("missing routing rules: %#v", routing["rules"])
	}
	privateRule := nestedMapFromAny(t, rules[0])
	if privateRule["outboundTag"] != "blocked" {
		t.Fatalf("expected private rule to block, got %#v", privateRule)
	}
	ips, ok := privateRule["ip"].([]any)
	if !ok || !containsAny(ips, "geoip:private") || !containsAny(ips, "100.64.0.0/10") || !containsAny(ips, "fc00::/7") {
		t.Fatalf("private routing rule missing expected ranges: %#v", privateRule["ip"])
	}
	inbounds, ok := doc["inbounds"].([]any)
	if !ok || len(inbounds) != 1 {
		t.Fatalf("expected exactly one inbound (xhttp-in), got %#v", doc["inbounds"])
	}
	xhttpIn := outboundByTag(t, inbounds, "xhttp-in")
	xhttpSockopt := nestedMap(t, nestedMap(t, xhttpIn, "streamSettings"), "sockopt")
	trustedXFF, ok := xhttpSockopt["trustedXForwardedFor"].([]any)
	if !ok || !containsAny(trustedXFF, "X-Real-IP") {
		t.Fatalf("expected xhttp-in trustedXForwardedFor to include X-Real-IP, got %#v", xhttpSockopt["trustedXForwardedFor"])
	}
}

func TestXrayShortIDsJSON(t *testing.T) {
	t.Setenv("XRAY_SHORT_IDS", "0123456789abcdef, abcdef")

	got, err := xrayShortIDsJSON()
	if err != nil {
		t.Fatalf("xrayShortIDsJSON: %v", err)
	}
	if got != `["0123456789abcdef","abcdef"]` {
		t.Fatalf("unexpected short IDs JSON: %s", got)
	}
	if shortID := xrayClientShortID(); shortID != "0123456789abcdef" {
		t.Fatalf("unexpected client short ID: %s", shortID)
	}
}

func outboundByTag(t *testing.T, outbounds []any, tag string) map[string]any {
	t.Helper()
	for _, outbound := range outbounds {
		doc := nestedMapFromAny(t, outbound)
		if doc["tag"] == tag {
			return doc
		}
	}
	t.Fatalf("missing outbound tag %q", tag)
	return nil
}

func assertXHTTPSockopt(t *testing.T, outbound map[string]any, want string) {
	t.Helper()
	streamSettings := nestedMap(t, outbound, "streamSettings")
	sockopt := nestedMap(t, streamSettings, "sockopt")
	if sockopt["domainStrategy"] != want {
		t.Fatalf("unexpected domainStrategy for %v: %#v", outbound["tag"], sockopt["domainStrategy"])
	}
	happyEyeballs := nestedMap(t, sockopt, "happyEyeballs")
	if happyEyeballs["tryDelayMs"] != float64(1) {
		t.Fatalf("unexpected happyEyeballs.tryDelayMs: %#v", happyEyeballs["tryDelayMs"])
	}
}

func nestedMap(t *testing.T, doc map[string]any, key string) map[string]any {
	t.Helper()
	child, ok := doc[key].(map[string]any)
	if !ok {
		t.Fatalf("unexpected nested map for %q: %#v", key, doc[key])
	}
	return child
}

func nestedMapFromAny(t *testing.T, value any) map[string]any {
	t.Helper()
	doc, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("unexpected map value: %#v", value)
	}
	return doc
}

func containsAny(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
