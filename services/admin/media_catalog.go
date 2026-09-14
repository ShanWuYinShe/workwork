package main

import (
	"admin/internal/platform"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// media_catalog.go：admin 侧的“分类审核（catalog 人工审核：导入候选 → 审核 → 上线 approved）”管理端点。
// 设计为 Option C：media_server 是 catalog.db 的唯一读写者，对内网暴露 /internal/*
// 管理接口；admin 仅作代理（requireRole("admin")）并转换响应信封，避免跨容器共享 SQLite 写。

const (
	defaultMediaCenterInternalURL = "http://media_server:8787"
	mediaCenterInternalTimeout    = 35 * time.Second
	mediaCenterInternalMaxBody    = 8 << 20 // 8 MiB：审校列表可能较大
)

// errMediaCenterInternalDisabled 表示未配置 MEDIA_SERVER_INTERNAL_TOKEN，内部管理接口不可用。
var errMediaCenterInternalDisabled = errors.New("media center internal API disabled")

var mediaCenterInternalClient = &http.Client{Timeout: mediaCenterInternalTimeout}

func mediaCenterInternalBaseURL() string {
	return strings.TrimRight(envOrDefault("MEDIA_CENTER_INTERNAL_URL", defaultMediaCenterInternalURL), "/")
}

func mediaCenterInternalToken() string {
	return strings.TrimSpace(platform.GetEnv("MEDIA_SERVER_INTERNAL_TOKEN"))
}

// callMediaCenterInternal 向 media_server 内部管理接口发起带令牌的请求，返回状态码与响应体。
func callMediaCenterInternal(ctx context.Context, method, path string, query url.Values, body []byte) (int, []byte, error) {
	token := mediaCenterInternalToken()
	if token == "" {
		return 0, nil, errMediaCenterInternalDisabled
	}
	target := mediaCenterInternalBaseURL() + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := mediaCenterInternalClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, mediaCenterInternalMaxBody))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, payload, nil
}

// mediaCenterInternalErrorMessage 从 media_server 的错误信封 {"error":{"code","message"}} 提取
// 面向用户的消息;无法解析时回退为 HTTP 状态文本。
func mediaCenterInternalErrorMessage(payload []byte, status int) string {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &env); err == nil {
		if msg := strings.TrimSpace(env.Error.Message); msg != "" {
			return msg
		}
	}
	if text := strings.TrimSpace(http.StatusText(status)); text != "" {
		return text
	}
	return "媒体中心内部接口返回错误"
}

// proxyMediaCenterInternal 调用内部接口并把结果转写为 admin 统一信封：
// 成功时把上游 JSON 原样作为 data 透传;失败时提取上游消息用 platform.WriteError 返回对应状态码。
func proxyMediaCenterInternal(w http.ResponseWriter, ctx context.Context, method, path string, query url.Values, body []byte) {
	status, payload, err := callMediaCenterInternal(ctx, method, path, query, body)
	if err != nil {
		if errors.Is(err, errMediaCenterInternalDisabled) {
			platform.WriteError(w, "媒体中心内部管理接口未启用：请在 .env 配置 MEDIA_SERVER_INTERNAL_TOKEN 并重建 admin/media_server", http.StatusServiceUnavailable)
			return
		}
		platform.WriteInternalError(w, err)
		return
	}
	if status < 200 || status >= 300 {
		// 上游 5xx 一律按内部错误处理:错误消息可能含表名/SQL/内部路径等实现细节,
		// 透传给前端会泄露内部状态;仅 4xx 业务错误(如 not_found)保留上游消息。
		if status >= 500 {
			log.Printf("media center internal %s %s: status=%d body=%s", method, path, status, string(payload))
			platform.WriteInternalError(w, errors.New("media center internal API error"))
			return
		}
		platform.WriteError(w, mediaCenterInternalErrorMessage(payload, status), status)
		return
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		platform.WriteJSON(w, struct{}{})
		return
	}
	if !json.Valid(payload) {
		platform.WriteInternalError(w, errors.New("media center internal API returned non-JSON body"))
		return
	}
	platform.WriteJSON(w, json.RawMessage(payload))
}

// apiMediaCatalogOverview 代理 GET /internal/catalog/overview：各分类审校计数 + 快照概况 + 维护操作状态。
func apiMediaCatalogOverview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodGet, "/internal/catalog/overview", nil, nil)
	}
}

// apiMediaCatalogItems 代理 GET /internal/catalog/items：列出某分类(可选状态)的成员供审校。
func apiMediaCatalogItems() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		category := strings.TrimSpace(r.URL.Query().Get("category"))
		if category == "" {
			platform.WriteError(w, "category 参数不能为空", http.StatusBadRequest)
			return
		}
		q := url.Values{}
		q.Set("category", category)
		if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
			q.Set("status", status)
		}
		if limit := strings.TrimSpace(r.URL.Query().Get("limit")); limit != "" {
			q.Set("limit", limit)
		}
		if offset := strings.TrimSpace(r.URL.Query().Get("offset")); offset != "" {
			q.Set("offset", offset)
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodGet, "/internal/catalog/items", q, nil)
	}
}

// apiMediaCatalogExport 代理 GET /internal/catalog/export：导出全部(或某分类/某状态)成员为
// 导入兼容的 JSON,供把分类数据部署到其他 media_server 实例(把导出 JSON 直接 import 即可)。
func apiMediaCatalogExport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := url.Values{}
		if category := strings.TrimSpace(r.URL.Query().Get("category")); category != "" {
			q.Set("category", category)
		}
		if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
			q.Set("status", status)
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodGet, "/internal/catalog/export", q, nil)
	}
}

// apiMediaCatalogReview 代理 POST /internal/catalog/review：对单个成员 approve/reject/置顶/排序。
// admin 强制把 reviewer 覆盖为当前登录用户名（审计），忽略客户端自带的 reviewer 字段。
func apiMediaCatalogReview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		var req map[string]json.RawMessage
		if err := json.Unmarshal(raw, &req); err != nil {
			platform.WriteError(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		_, username := getUserFromContext(r)
		if reviewer, mErr := json.Marshal(strings.TrimSpace(username)); mErr == nil {
			req["reviewer"] = reviewer
		}
		body, err := json.Marshal(req)
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodPost, "/internal/catalog/review", nil, body)
	}
}

// apiMediaCatalogDelete 代理 POST /internal/catalog/delete：硬删除某分类下的一个视频成员
// (增删改查的"删",区别于 reject 软标记)。body 需含 categoryId/provider/nativeId。
func apiMediaCatalogDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		if !json.Valid(bytes.TrimSpace(raw)) {
			platform.WriteError(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodPost, "/internal/catalog/delete", nil, bytes.TrimSpace(raw))
	}
}

// apiMediaCatalogImport 代理 POST /internal/catalog/import：导入外部工具/AI 收集好的候选。
// 默认导入为 pending；item 可带 status=approved 直接收录上线（用于"已审核视频集一步上线"）。
// admin 强制把 reviewer 覆盖为当前登录用户名（审计），用于 approved 项的审校留痕。
func apiMediaCatalogImport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			platform.WriteError(w, "请求体必须是包含 items 的 JSON 对象", http.StatusBadRequest)
			return
		}
		var req map[string]json.RawMessage
		if err := json.Unmarshal(bytes.TrimSpace(raw), &req); err != nil {
			platform.WriteError(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		if req == nil {
			platform.WriteError(w, "请求体必须是包含 items 的 JSON 对象", http.StatusBadRequest)
			return
		}
		_, username := getUserFromContext(r)
		if reviewer, mErr := json.Marshal(strings.TrimSpace(username)); mErr == nil {
			req["reviewer"] = reviewer
		}
		body, err := json.Marshal(req)
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodPost, "/internal/catalog/import", nil, body)
	}
}
