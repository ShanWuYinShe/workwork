package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// webhookTimeout 限制单次通知的最长耗时;通知绝不能拖慢主流程。
const webhookTimeout = 5 * time.Second

// webhookDedupeWindow 内同一 (event, summary) 只发一次:撞库风暴下每个 IP
// 封禁各发一条会刷屏淹没真实告警(对接 Telegram/企微时尤甚)。窗口过后同
// 事件再次发生仍会通知——抑制的是重复刷屏,不是事件本身。
const webhookDedupeWindow = time.Minute

var (
	webhookDedupeMu sync.Mutex
	// key: event + "\x00" + summary → 上次发送时间。
	webhookLastSent = map[string]time.Time{}
)

// webhookShouldSend 报告该事件当前是否应发送,应发时记录本次发送时间。
// 相同 (event, summary) 在抑制窗口内只发一次;条目惰性清理——summary 可能
// 含 IP 等高基数片段,过期条目随查询删除,防 map 无界增长。
func webhookShouldSend(event, summary string, now time.Time) bool {
	key := event + "\x00" + summary
	webhookDedupeMu.Lock()
	defer webhookDedupeMu.Unlock()
	for k, t := range webhookLastSent {
		if now.Sub(t) >= webhookDedupeWindow {
			delete(webhookLastSent, k)
		}
	}
	if last, ok := webhookLastSent[key]; ok && now.Sub(last) < webhookDedupeWindow {
		return false
	}
	webhookLastSent[key] = now
	return true
}

// webhookPayload 是发往 WEBHOOK_URL 的事件信封。字段刻意保持扁平通用,
// 兼容 Telegram bot / 企业微信 / 自建接收器等仅做转发展示的场景。
type webhookPayload struct {
	Event   string            `json:"event"`
	Summary string            `json:"summary"`
	Details map[string]string `json:"details,omitempty"`
	Time    string            `json:"time"`
}

// NotifyWebhook 异步向 WEBHOOK_URL 推送关键事件(安全封禁、自愈达到上限、
// 部署失败等)。未配置 URL 时为 no-op;发送失败只记日志,绝不影响主流程——
// 通知是旁路,不能因为通知挂掉而阻塞封禁/重启。相同 (event, summary) 在
// 抑制窗口内只发一次(防风暴刷屏),窗口过后再次发生仍会通知。
func NotifyWebhook(event, summary string, details map[string]string) {
	url := GetEnv("WEBHOOK_URL")
	if url == "" {
		return
	}
	if !webhookShouldSend(event, summary, time.Now()) {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[webhook] panic: %v", r)
			}
		}()
		body, err := json.Marshal(webhookPayload{
			Event:   event,
			Summary: summary,
			Details: details,
			Time:    time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			log.Printf("[webhook] marshal %s: %v", event, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), webhookTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			log.Printf("[webhook] build request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[webhook] deliver %s: %v", event, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf("[webhook] deliver %s: HTTP %d", event, resp.StatusCode)
		}
	}()
}
