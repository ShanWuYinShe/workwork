package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// webhookTimeout 限制单次通知的最长耗时;通知绝不能拖慢主流程。
const webhookTimeout = 5 * time.Second

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
// 通知是旁路,不能因为通知挂掉而阻塞封禁/重启。
func NotifyWebhook(event, summary string, details map[string]string) {
	url := GetEnv("WEBHOOK_URL")
	if url == "" {
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
