package platform

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestNotifyWebhookDeliversEventPayload 锁定事件信封形态与投递行为:
// 配置 WEBHOOK_URL 后异步 POST JSON,接收方收到的 event/summary/details 完整。
func TestNotifyWebhookDeliversEventPayload(t *testing.T) {
	var mu sync.Mutex
	var got webhookPayload
	called := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("期望 POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("期望 application/json, got %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		mu.Lock()
		called <- struct{}{}
		mu.Unlock()
	}))
	defer srv.Close()

	t.Setenv("WEBHOOK_URL", srv.URL)
	NotifyWebhook("security.block", "封禁 198.51.100.7", map[string]string{"ip": "198.51.100.7", "reason": "brute force"})
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("webhook 未在超时内投递")
	}
	if got.Event != "security.block" || got.Summary == "" || got.Details["ip"] != "198.51.100.7" {
		t.Fatalf("payload 不完整: %+v", got)
	}
	if got.Time == "" {
		t.Fatal("缺少事件时间")
	}
}

// TestNotifyWebhookNoopWithoutURL:未配置 WEBHOOK_URL 时必须是 no-op,
// 不 panic 不阻塞(全局函数,无法观测网络行为,这里验证不炸即可)。
func TestNotifyWebhookNoopWithoutURL(t *testing.T) {
	t.Setenv("WEBHOOK_URL", "")
	done := make(chan struct{})
	go func() {
		NotifyWebhook("test.event", "无 URL", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("未配置 URL 时 NotifyWebhook 应立即返回")
	}
}

// TestWebhookMTLSClientPresentsCert:ALERT_MTLS_CERT/KEY 指向有效证书时,构造的
// client 出示该证书(portal 网关边缘 mTLS 场景)。注:不在此做真实 TLS 握手——
// darwin 的系统证书池走钥匙串、无视 SSL_CERT_FILE,集成式断言不可移植;装配逻辑
// 是本次新增的全部内容,握手本身是标准库行为。
func TestWebhookMTLSClientPresentsCert(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "workwork-admin"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("sign cert: %v", err)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	t.Setenv("ALERT_MTLS_CERT", certFile)
	t.Setenv("ALERT_MTLS_KEY", keyFile)
	c := webhookMTLSClient()
	if c == http.DefaultClient {
		t.Fatal("证书齐备时应构造持证 client,而非 DefaultClient")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || len(tr.TLSClientConfig.Certificates) != 1 {
		t.Fatalf("Transport 未装配客户端证书: %#v", c.Transport)
	}
	if string(tr.TLSClientConfig.Certificates[0].Certificate[0]) != string(der) {
		t.Fatal("装配的不是 ALERT_MTLS_CERT 指定的证书")
	}
}

// TestWebhookMTLSClientFallsBack:证书缺失或损坏时回落 DefaultClient(无证推送,
// 只记日志不炸)。
func TestWebhookMTLSClientFallsBack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ALERT_MTLS_CERT", filepath.Join(dir, "nope.pem"))
	t.Setenv("ALERT_MTLS_KEY", filepath.Join(dir, "nope.pem"))
	if c := webhookMTLSClient(); c != http.DefaultClient {
		t.Fatal("证书缺失时应回落 DefaultClient")
	}
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not-a-cert"), 0600); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	t.Setenv("ALERT_MTLS_CERT", bad)
	t.Setenv("ALERT_MTLS_KEY", bad)
	if c := webhookMTLSClient(); c != http.DefaultClient {
		t.Fatal("证书损坏时应回落 DefaultClient")
	}
}

// TestNotifyWebhookFallsBackWithoutClientCert:未配 ALERT_MTLS_* 时照常无证推送。
func TestNotifyWebhookFallsBackWithoutClientCert(t *testing.T) {
	called := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			t.Errorf("无证推送不应使用 TLS")
		}
		called <- struct{}{}
	}))
	defer srv.Close()
	t.Setenv("ALERT_MTLS_CERT", "")
	t.Setenv("ALERT_MTLS_KEY", "")
	t.Setenv("WEBHOOK_URL", srv.URL)
	NotifyWebhook("security.block", "无证推送探测", nil)
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("无证推送未送达")
	}
}

// TestNotifyWebhookDeliveryFailureIsLoggedNotFatal:接收端 500 时只记日志,
// 不向调用方传播错误。
func TestNotifyWebhookDeliveryFailureIsLoggedNotFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("WEBHOOK_URL", srv.URL)
	done := make(chan struct{})
	go func() {
		NotifyWebhook("test.event", "接收端 500", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("投递失败不应阻塞调用方")
	}
}
