package accounts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateClientConfigXHTTPOnly(t *testing.T) {
	cfgJSON := GenerateClientConfig([]map[string]string{{"id": "uuid-1"}}, ClientConfig{
		XHTTPDomain:    "example.com",
		XHTTPProxyAuth: "viewer:secret",
	})

	var doc map[string]any
	if err := json.Unmarshal([]byte(cfgJSON), &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	outbounds, ok := doc["outbounds"].([]any)
	if !ok || len(outbounds) != 3 {
		t.Fatalf("expected xhttp/direct/reject outbounds, got %#v", doc["outbounds"])
	}

	xhttp := outboundByTag(t, outbounds, "xhttp")
	assertXHTTPSockopt(t, xhttp, "UseIP")

	xhttpSettings := nestedMap(t, nestedMap(t, xhttp, "streamSettings"), "xhttpSettings")
	headers := nestedMap(t, xhttpSettings, "headers")
	if headers["Authorization"] == "" {
		t.Fatal("expected xhttp auth header to be present")
	}
	streamSettings := nestedMap(t, xhttp, "streamSettings")
	if streamSettings["security"] != "tls" {
		t.Fatalf("expected xhttp outbound security tls, got %#v", streamSettings["security"])
	}

	// 未配置 XHTTP_ADDRESS 时,出站地址必须跟随 XHTTP_DOMAIN,SNI 同域。
	vnext, ok := nestedMap(t, xhttp, "settings")["vnext"].([]any)
	if !ok || len(vnext) != 1 {
		t.Fatalf("expected single vnext entry, got %#v", nestedMap(t, xhttp, "settings")["vnext"])
	}
	if got := nestedMapFromAny(t, vnext[0])["address"]; got != "example.com" {
		t.Fatalf("expected default address to follow XHTTP_DOMAIN, got %#v", got)
	}
	if got := nestedMap(t, streamSettings, "tlsSettings")["serverName"]; got != "example.com" {
		t.Fatalf("expected serverName to follow XHTTP_DOMAIN, got %#v", got)
	}
	// 默认 ALPN 是 h2(TCP),保留经线上验证的 TCP 调优 sockopt。
	if got := nestedMap(t, streamSettings, "tlsSettings")["alpn"]; !containsAny(got.([]any), "h2") {
		t.Fatalf("expected default alpn h2, got %#v", got)
	}
	sockoptDefault := nestedMap(t, streamSettings, "sockopt")
	if sockoptDefault["tcpFastOpen"] != true || sockoptDefault["tcpCongestion"] != "bbr" {
		t.Fatalf("expected default h2 sockopt to keep TCP tuning, got %#v", sockoptDefault)
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
	// DoH 上游必须是国内直连可达的加密解析：境外 DoH(如 1.1.1.1)在国内
	// 网络超时，每轮解析都会刷 [Error] 日志且永远不中奖。
	if servers[0] != "https+local://223.5.5.5/dns-query" {
		t.Fatalf("expected AliDNS DoH as first resolver, got %#v", servers[0])
	}
	for _, s := range servers {
		if s == "https+local://1.1.1.1/dns-query" {
			t.Fatalf("1.1.1.1 DoH is unreachable in CN, got %#v", servers)
		}
	}
	// 加密支路必须互为备份:只有一条 DoH 时该上游故障即静默退回明文解析。
	if !containsAny(servers, "https+local://1.12.12.12/dns-query") {
		t.Fatalf("expected DNSPod DoH as backup resolver, got %#v", servers)
	}
	// 明文支路覆盖国内递归 + 境外兜底。
	for _, want := range []string{"223.5.5.5", "119.29.29.29", "8.8.8.8"} {
		if !containsAny(servers, want) {
			t.Fatalf("expected plain resolver %s, got %#v", want, servers)
		}
	}
	if dns["enableParallelQuery"] != true {
		t.Fatalf("expected client dns parallel query enabled, got %#v", dns["enableParallelQuery"])
	}

	routing := nestedMap(t, doc, "routing")
	if _, hasBalancer := routing["balancers"]; hasBalancer {
		t.Fatalf("expected no balancer (single xhttp outbound), got %#v", routing["balancers"])
	}
	rules, ok := routing["rules"].([]any)
	if !ok || len(rules) < 2 {
		t.Fatalf("unexpected routing rules: %#v", routing["rules"])
	}
	proxyRule := nestedMapFromAny(t, rules[1])
	if proxyRule["outboundTag"] != "xhttp" {
		t.Fatalf("expected proxy rule to point at xhttp outbound, got %#v", proxyRule)
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

	// Reality 已移除：配置不得再包含 observatory（单出站无需探测均衡）。
	if _, hasObservatory := doc["observatory"]; hasObservatory {
		t.Fatal("expected no observatory section in XHTTP-only config")
	}

	// XHTTP 域名缺失即视为服务端配置不完整。
	if got := GenerateClientConfig(nil, ClientConfig{}); !strings.Contains(got, "incomplete") {
		t.Fatalf("expected incomplete-config error, got %s", got)
	}
}

func TestEncodeXrayClientsOmitsOwner(t *testing.T) {
	clients := []xrayClientEntry{{ID: "uuid-1", Owner: "viewer", Email: "viewer"}}
	got, err := encodeXrayClients(clients)
	if err != nil {
		t.Fatalf("encodeXrayClients: %v", err)
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
	// XHTTP 入站不使用 flow（vision flow 属 Reality，已移除）。
	if arr[0]["flow"] != "" {
		t.Fatalf("expected empty flow, got %s", got)
	}
	if got, err := encodeXrayClients(nil); err != nil || got != "[]" {
		t.Fatalf("expected empty clients to encode as [], got %q, err %v", got, err)
	}
}

func TestXrayTemplateRendersValidJSON(t *testing.T) {
	templatePath := filepath.Join("..", "..", "configs", "xray.json.tmpl")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}

	rendered := strings.ReplaceAll(string(data),
		"__XRAY_CLIENTS_JSON__", `[{"id":"uuid-1","email":"viewer","flow":""}]`)
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
	// 服务端解析器要覆盖两个独立 DoH 厂商 + 明文兜底 + 系统解析器:单厂商
	// (如 Cloudflare)故障时仍有 Google 支路,不会让 freedom 出站的域名解析
	// 整体失败——服务端在境外,1.1.1.1 可用,与客户端(国内,禁用境外 DoH)不同。
	serverDNS, ok := dns["servers"].([]any)
	if !ok || len(serverDNS) < 4 {
		t.Fatalf("expected server dns servers, got %#v", dns["servers"])
	}
	if serverDNS[0] != "https+local://1.1.1.1/dns-query" {
		t.Fatalf("expected Cloudflare DoH as first resolver, got %#v", serverDNS[0])
	}
	if !containsAny(serverDNS, "https+local://8.8.8.8/dns-query") {
		t.Fatalf("expected an independent DoH provider, got %#v", serverDNS)
	}
	for _, want := range []string{"1.1.1.1", "8.8.8.8", "localhost"} {
		if !containsAny(serverDNS, want) {
			t.Fatalf("expected plain resolver %s, got %#v", want, serverDNS)
		}
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
	// rules[0] 是 stats API 回环规则(api inbound → 内置 api outbound),必须
	// 排在 private blocked 之前——api 流量 dest 是 127.0.0.1,否则会被
	// geoip:private 规则吃掉导致 StatsService 永远不可达。
	apiRule := nestedMapFromAny(t, rules[0])
	if apiRule["outboundTag"] != "api" {
		t.Fatalf("expected first rule to route api inbound, got %#v", apiRule)
	}
	privateRule := nestedMapFromAny(t, rules[1])
	if privateRule["outboundTag"] != "blocked" {
		t.Fatalf("expected private rule to block, got %#v", privateRule)
	}
	ips, ok := privateRule["ip"].([]any)
	if !ok || !containsAny(ips, "geoip:private") || !containsAny(ips, "100.64.0.0/10") || !containsAny(ips, "fc00::/7") {
		t.Fatalf("private routing rule missing expected ranges: %#v", privateRule["ip"])
	}
	// Reality 入站已移除：仅 xhttp-in + api。
	inbounds, ok := doc["inbounds"].([]any)
	if !ok || len(inbounds) != 2 {
		t.Fatalf("expected xhttp-in + api inbounds, got %#v", doc["inbounds"])
	}
	if strings.Contains(rendered, `"reality"`) {
		t.Fatal("rendered template must not contain reality settings")
	}
	xhttpIn := outboundByTag(t, inbounds, "xhttp-in")
	xhttpSockopt := nestedMap(t, nestedMap(t, xhttpIn, "streamSettings"), "sockopt")
	trustedXFF, ok := xhttpSockopt["trustedXForwardedFor"].([]any)
	if !ok || !containsAny(trustedXFF, "X-Real-IP") {
		t.Fatalf("expected xhttp-in trustedXForwardedFor to include X-Real-IP, got %#v", xhttpSockopt["trustedXForwardedFor"])
	}
	// 分片契约:服务端 scMaxEachPostBytes 必须显式配置且严格大于 Xray 客户端
	// 默认值(GetNormalizedScMaxEachPostBytes 回落 1000000)。仅"相等"是贴边
	// 自洽——任何客户端把分片调大就会被服务端 413 且不读 body,客户端上行
	// 静默停摆(表现为响应体停滞),而该日志在生产 loglevel=warning 下不可见。
	xhttpSettings := nestedMap(t, nestedMap(t, xhttpIn, "streamSettings"), "xhttpSettings")
	if xhttpSettings["mode"] != "packet-up" {
		t.Fatalf("xhttp-in mode must stay packet-up, got %#v", xhttpSettings["mode"])
	}
	scMax, ok := xhttpSettings["scMaxEachPostBytes"].(float64)
	if !ok || scMax <= 1000000 {
		t.Fatalf("xhttp-in scMaxEachPostBytes must exceed client default 1000000, got %#v", xhttpSettings["scMaxEachPostBytes"])
	}
	if posts, ok := xhttpSettings["scMaxBufferedPosts"].(float64); !ok || posts <= 0 {
		t.Fatalf("xhttp-in scMaxBufferedPosts must be positive, got %#v", xhttpSettings["scMaxBufferedPosts"])
	}
}

func TestGenerateClientConfigPreferredAddress(t *testing.T) {
	// XHTTP_ADDRESS 指向 Cloudflare 优选入口:出站地址换成优选域名,SNI/证书名
	// 必须仍是 XHTTP_DOMAIN——优选入口是 DNS-only 记录,没有边缘证书,当 SNI
	// 用会握手失败。
	cfgJSON := GenerateClientConfig([]map[string]string{{"id": "uuid-1"}}, ClientConfig{
		XHTTPDomain:  "example.com",
		XHTTPAddress: "opt.example.com",
	})
	var doc map[string]any
	if err := json.Unmarshal([]byte(cfgJSON), &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	outbounds, ok := doc["outbounds"].([]any)
	if !ok {
		t.Fatalf("unexpected outbounds: %#v", doc["outbounds"])
	}
	xhttp := outboundByTag(t, outbounds, "xhttp")
	streamSettings := nestedMap(t, xhttp, "streamSettings")
	if got := nestedMap(t, streamSettings, "tlsSettings")["serverName"]; got != "example.com" {
		t.Fatalf("preferred address must not leak into SNI, got %#v", got)
	}
	vnext, ok := nestedMap(t, xhttp, "settings")["vnext"].([]any)
	if !ok || len(vnext) != 1 {
		t.Fatalf("expected single vnext entry, got %#v", nestedMap(t, xhttp, "settings")["vnext"])
	}
	if got := nestedMapFromAny(t, vnext[0])["address"]; got != "opt.example.com" {
		t.Fatalf("expected preferred address, got %#v", got)
	}

	// 纯空白地址视为未配置(避免 .env 里的尾随空格把出站地址清空)。
	blank := GenerateClientConfig([]map[string]string{{"id": "uuid-1"}}, ClientConfig{
		XHTTPDomain:  "example.com",
		XHTTPAddress: "  ",
	})
	var blankDoc map[string]any
	if err := json.Unmarshal([]byte(blank), &blankDoc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	blankOutbounds := blankDoc["outbounds"].([]any)
	blankVnext := nestedMap(t, outboundByTag(t, blankOutbounds, "xhttp"), "settings")["vnext"].([]any)
	if got := nestedMapFromAny(t, blankVnext[0])["address"]; got != "example.com" {
		t.Fatalf("expected blank address to fall back to domain, got %#v", got)
	}
}

func TestGenerateClientConfigALPNH3(t *testing.T) {
	// XHTTP_ALPN=h3:ALPN 换成 h3(QUIC),TCP 专属 sockopt 必须整体剔除——
	// 它们对 QUIC 出站不生效,留着只会让配置携带死参数。
	cfgJSON := GenerateClientConfig([]map[string]string{{"id": "uuid-1"}}, ClientConfig{
		XHTTPDomain: "example.com",
		XHTTPALPN:   "h3",
	})
	var doc map[string]any
	if err := json.Unmarshal([]byte(cfgJSON), &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	xhttp := outboundByTag(t, doc["outbounds"].([]any), "xhttp")
	streamSettings := nestedMap(t, xhttp, "streamSettings")
	alpn := nestedMap(t, streamSettings, "tlsSettings")["alpn"].([]any)
	if len(alpn) != 1 || alpn[0] != "h3" {
		t.Fatalf("expected alpn [h3], got %#v", alpn)
	}
	sockopt := nestedMap(t, streamSettings, "sockopt")
	if sockopt["domainStrategy"] != "UseIP" {
		t.Fatalf("expected UseIP on h3 sockopt, got %#v", sockopt)
	}
	if _, ok := sockopt["happyEyeballs"]; !ok {
		t.Fatalf("expected happyEyeballs on h3 sockopt, got %#v", sockopt)
	}
	for _, banned := range []string{"tcpFastOpen", "tcpCongestion", "tcpKeepAliveIdle", "tcpKeepAliveInterval", "tcpUserTimeout"} {
		if _, exists := sockopt[banned]; exists {
			t.Fatalf("h3 (QUIC) outbound must not carry TCP-only sockopt %s, got %#v", banned, sockopt)
		}
	}
}

func TestValidateProxyConfigEnvAddress(t *testing.T) {
	t.Setenv("XHTTP_DOMAIN", "example.com")
	// 带 scheme/路径/空白的地址会被原样写进 vnext.address,只允许裸主机名或 IP。
	for _, bad := range []string{"https://opt.example.com", "opt.example.com/path", "opt example.com"} {
		t.Setenv("XHTTP_ADDRESS", bad)
		if err := ValidateProxyConfigEnv(); err == nil {
			t.Fatalf("expected XHTTP_ADDRESS=%q to be rejected", bad)
		}
	}
	t.Setenv("XHTTP_ADDRESS", "opt.example.com")
	if err := ValidateProxyConfigEnv(); err != nil {
		t.Fatalf("expected preferred address to pass, got %v", err)
	}
	t.Setenv("XHTTP_ADDRESS", "")
	if err := ValidateProxyConfigEnv(); err != nil {
		t.Fatalf("expected empty XHTTP_ADDRESS to pass, got %v", err)
	}
	// ALPN 白名单:空/h2/h3 通过,其他值生成不可协商的配置,启动期拒绝。
	t.Setenv("XHTTP_ALPN", "h3")
	if err := ValidateProxyConfigEnv(); err != nil {
		t.Fatalf("expected XHTTP_ALPN=h3 to pass, got %v", err)
	}
	t.Setenv("XHTTP_ALPN", "quic")
	if err := ValidateProxyConfigEnv(); err == nil {
		t.Fatal("expected XHTTP_ALPN=quic to be rejected")
	}
	t.Setenv("XHTTP_ALPN", "")
	if err := ValidateProxyConfigEnv(); err != nil {
		t.Fatalf("expected empty XHTTP_ALPN to pass, got %v", err)
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
