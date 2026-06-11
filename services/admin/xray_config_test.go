package main

import (
	"admin/handlers"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateClientConfigIncludesDualTransportTuning(t *testing.T) {
	cfgJSON := handlers.GenerateClientConfig([]map[string]string{{"id": "uuid-1"}}, handlers.ClientConfig{
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
	if !ok || len(outbounds) < 2 {
		t.Fatalf("unexpected outbounds: %#v", doc["outbounds"])
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

	routing := nestedMap(t, doc, "routing")
	rules, ok := routing["rules"].([]any)
	if !ok || len(rules) < 2 {
		t.Fatalf("unexpected routing rules: %#v", routing["rules"])
	}
	proxyRule := nestedMapFromAny(t, rules[1])
	domains, ok := proxyRule["domain"].([]any)
	if !ok {
		t.Fatalf("unexpected proxy domains: %#v", proxyRule["domain"])
	}
	if !containsAny(domains, "geosite:youtube") {
		t.Fatalf("expected proxy domains to include geosite:youtube, got %#v", domains)
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
}

func TestXrayTemplateRendersValidJSON(t *testing.T) {
	templatePath := filepath.Join("..", "xray", "config.json.tmpl")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}

	rendered := string(data)
	replacements := map[string]string{
		"__XRAY_CLIENTS_JSON__":         `[{"id":"uuid-1","email":"viewer","flow":"","owner":"viewer"}]`,
		"__XRAY_REALITY_CLIENTS_JSON__": `[{"id":"uuid-1","email":"viewer","flow":"xtls-rprx-vision","owner":"viewer"}]`,
		"__XRAY_REALITY_DEST__":         "addons.mozilla.org:443",
		"__XRAY_REALITY_SERVER_NAME__":  "addons.mozilla.org",
		"__XRAY_PRIVATE_KEY__":          "private-key",
		"__XRAY_SHORT_IDS_JSON__":       `["0123456789abcdef","abcdef"]`,
	}
	for placeholder, value := range replacements {
		rendered = strings.ReplaceAll(rendered, placeholder, value)
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
	reality := outboundByTag(t, doc["inbounds"].([]any), "reality-in")
	realitySettings := nestedMap(t, nestedMap(t, reality, "streamSettings"), "realitySettings")
	shortIDs, ok := realitySettings["shortIds"].([]any)
	if !ok || len(shortIDs) != 2 {
		t.Fatalf("unexpected reality shortIds: %#v", realitySettings["shortIds"])
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
