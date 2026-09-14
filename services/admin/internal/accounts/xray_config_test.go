package accounts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateClientConfigIncludesDualTransportTuning(t *testing.T) {
	cfgJSON := GenerateClientConfig([]map[string]string{{"id": "uuid-1"}}, ClientConfig{
		RealityDomain:     "xray.example.com",
		RealityServerName: "addons.mozilla.org",
		RealityPublicKey:  "public-key",
		RealityShortID:    "0123456789abcdef",
		XHTTPDomain:       "example.com",
		XHTTPProxyAuth:    "viewer:secret",
	})

	var doc map[string]any
	if err := json.Unmarshal([]byte(cfgJSON), &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	outbounds, ok := doc["outbounds"].([]any)
	if !ok || len(outbounds) != 4 {
		t.Fatalf("expected reality/xhttp/direct/reject outbounds, got %#v", doc["outbounds"])
	}

	reality := outboundByTag(t, outbounds, "reality")
	xhttp := outboundByTag(t, outbounds, "xhttp")

	assertXHTTPSockopt(t, reality, "UseIP")
	assertXHTTPSockopt(t, xhttp, "UseIP")

	xhttpSettings := nestedMap(t, nestedMap(t, xhttp, "streamSettings"), "xhttpSettings")
	headers := nestedMap(t, xhttpSettings, "headers")
	if headers["Authorization"] == "" {
		t.Fatal("expected xhttp auth header to be present")
	}

	// 客户端内置 DNS：domainStrategy=UseIP 依赖它解析代理服务器域名。
	dns := nestedMap(t, doc, "dns")
	if dns["queryStrategy"] != "UseIP" {
		t.Fatalf("unexpected client dns query strategy: %#v", dns["queryStrategy"])
	}
	servers, ok := dns["servers"].([]any)
	if !ok || len(servers) == 0 {
		t.Fatalf("expected client dns servers, got %#v", dns["servers"])
	}
	if dns["enableParallelQuery"] != true {
		t.Fatalf("expected client dns parallel query enabled, got %#v", dns["enableParallelQuery"])
	}

	routing := nestedMap(t, doc, "routing")
	balancers, ok := routing["balancers"].([]any)
	if !ok || len(balancers) != 1 {
		t.Fatalf("expected exactly one balancer, got %#v", routing["balancers"])
	}
	balancer := nestedMapFromAny(t, balancers[0])
	selector, ok := balancer["selector"].([]any)
	if !ok || !containsAny(selector, "reality") || !containsAny(selector, "xhttp") {
		t.Fatalf("expected balancer selector [reality xhttp], got %#v", balancer["selector"])
	}
	rules, ok := routing["rules"].([]any)
	if !ok || len(rules) < 2 {
		t.Fatalf("unexpected routing rules: %#v", routing["rules"])
	}
	proxyRule := nestedMapFromAny(t, rules[1])
	if proxyRule["balancerTag"] != "proxy" {
		t.Fatalf("expected proxy rule to point at balancer, got %#v", proxyRule)
	}
	domains, ok := proxyRule["domain"].([]any)
	if !ok {
		t.Fatalf("unexpected proxy domains: %#v", proxyRule["domain"])
	}
	if !containsAny(domains, "geosite:youtube") {
		t.Fatalf("expected proxy domains to include geosite:youtube, got %#v", domains)
	}
	// 与实际在用的本地客户端分流列表对齐：缺任何一个都会导致面板重下的配置
	// 把对应流量漏到 direct。
	for _, want := range []string{
		"geosite:cloudflare", "geosite:mozilla", "geosite:figma", "geosite:npmjs",
		"domain:antigravity-unleash.goog", "domain:opencode.ai",
		"domain:investing.com", "domain:textnow.com", "domain:toutoupa.com",
	} {
		if !containsAny(domains, want) {
			t.Fatalf("expected proxy domains to include %s, got %#v", want, domains)
		}
	}

	observatory := nestedMap(t, doc, "observatory")
	if observatory["subjectSelector"] == nil {
		t.Fatalf("expected observatory subjectSelector, got %#v", observatory["subjectSelector"])
	}
	if observatory["probeURL"] != "https://cp.cloudflare.com/generate_204" {
		t.Fatalf("unexpected observatory probeURL: %#v", observatory["probeURL"])
	}
	if observatory["probeInterval"] != "30s" {
		t.Fatalf("unexpected observatory probeInterval: %#v", observatory["probeInterval"])
	}
	if observatory["enableConcurrency"] != true {
		t.Fatalf("unexpected observatory enableConcurrency: %#v", observatory["enableConcurrency"])
	}

	// Reality 字段缺失即视为服务端配置不完整。
	if got := GenerateClientConfig(nil, ClientConfig{XHTTPDomain: "example.com"}); !strings.Contains(got, "incomplete") {
		t.Fatalf("expected incomplete-config error, got %s", got)
	}
}

func TestEncodeXrayClientsOmitsOwner(t *testing.T) {
	clients := []xrayClientEntry{{ID: "uuid-1", Owner: "viewer", Email: "viewer"}}
	for _, flow := range []string{"", "xtls-rprx-vision"} {
		got, err := encodeXrayClients(clients, flow)
		if err != nil {
			t.Fatalf("encodeXrayClients(flow=%q): %v", flow, err)
		}
		if strings.Contains(got, "owner") {
			t.Fatalf("generated xray clients must not contain owner, got %s", got)
		}
		var arr []map[string]any
		if err := json.Unmarshal([]byte(got), &arr); err != nil {
			t.Fatalf("unmarshal %s: %v", got, err)
		}
		if len(arr) != 1 {
			t.Fatalf("expected 1 client, got %s", got)
		}
		for key := range arr[0] {
			if key != "id" && key != "email" && key != "flow" {
				t.Fatalf("unexpected client key %q in %s", key, got)
			}
		}
		if arr[0]["flow"] != flow {
			t.Fatalf("expected flow %q, got %s", flow, got)
		}
	}
	if got, err := encodeXrayClients(nil, ""); err != nil || got != "[]" {
		t.Fatalf("expected empty clients to encode as [], got %q, err %v", got, err)
	}
}

func TestXrayTemplateRendersValidJSON(t *testing.T) {
	templatePath := filepath.Join("..", "..", "configs", "xray.json.tmpl")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}

	rendered := string(data)
	replacements := map[string]string{
		"__XRAY_CLIENTS_JSON__":         `[{"id":"uuid-1","email":"viewer","flow":""}]`,
		"__XRAY_REALITY_CLIENTS_JSON__": `[{"id":"uuid-1","email":"viewer","flow":"xtls-rprx-vision"}]`,
		"__XRAY_REALITY_DEST__":         "addons.mozilla.org:443",
		"__XRAY_REALITY_SERVER_NAME__":  "addons.mozilla.org",
		"__XRAY_PRIVATE_KEY__":          "private-key",
		"__XRAY_SHORT_IDS_JSON__":       `["0123456789abcdef","abcdef"]`,
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
	if !ok || len(inbounds) != 2 {
		t.Fatalf("expected xhttp-in + reality-in inbounds, got %#v", doc["inbounds"])
	}
	reality := outboundByTag(t, inbounds, "reality-in")
	realitySettings := nestedMap(t, nestedMap(t, reality, "streamSettings"), "realitySettings")
	shortIDs, ok := realitySettings["shortIds"].([]any)
	if !ok || len(shortIDs) != 2 {
		t.Fatalf("unexpected reality shortIds: %#v", realitySettings["shortIds"])
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
	if shortID := XrayClientShortID(); shortID != "0123456789abcdef" {
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
