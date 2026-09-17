package platform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
