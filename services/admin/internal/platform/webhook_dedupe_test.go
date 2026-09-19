package platform

import (
	"testing"
	"time"
)

// TestWebhookShouldSendDeduplication 相同 (event, summary) 在抑制窗口内只发
// 一次;不同 summary(如含不同 IP)互不影响;窗口过期后可再次发送。
func TestWebhookShouldSendDeduplication(t *testing.T) {
	// 隔离全局状态:测试直接操纵包级 map,先清空并在结束后恢复。
	saved := webhookLastSent
	webhookLastSent = map[string]time.Time{}
	t.Cleanup(func() { webhookLastSent = saved })

	now := time.Now()
	if !webhookShouldSend("ratelimit.ban", "封禁: 1.2.3.4", now) {
		t.Fatal("first occurrence should send")
	}
	if webhookShouldSend("ratelimit.ban", "封禁: 1.2.3.4", now.Add(30*time.Second)) {
		t.Fatal("duplicate within window should be suppressed")
	}
	// 不同 summary(不同 IP)不受抑制。
	if !webhookShouldSend("ratelimit.ban", "封禁: 5.6.7.8", now.Add(30*time.Second)) {
		t.Fatal("different summary should not be suppressed")
	}
	// 窗口过期后同 summary 可再次发送。
	if !webhookShouldSend("ratelimit.ban", "封禁: 1.2.3.4", now.Add(2*webhookDedupeWindow)) {
		t.Fatal("occurrence after window should send again")
	}
}

// TestWebhookShouldSendCleanup 验证过期条目被清理,summary 高基数(含随机 IP)
// 时 map 不会无界增长。
func TestWebhookShouldSendCleanup(t *testing.T) {
	saved := webhookLastSent
	webhookLastSent = map[string]time.Time{}
	t.Cleanup(func() { webhookLastSent = saved })

	now := time.Now()
	for i := 0; i < 200; i++ {
		webhookShouldSend("evt", "ip-10.0.0."+time.Duration(i).String(), now)
	}
	// 时间推进超过窗口后再发一条:旧行全部清掉。
	webhookShouldSend("evt", "ip-10.0.0.new", now.Add(2*webhookDedupeWindow))
	if len(webhookLastSent) != 1 {
		t.Fatalf("stale entries should be pruned, got %d entries", len(webhookLastSent))
	}
}
