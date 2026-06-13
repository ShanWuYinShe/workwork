package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// media_catalog.go：admin 侧的“分类预采集（catalog 审校 + 运行时快照 + 本地审校 curation.json）”
// 管理端点。设计为 Option C：media_server 是 catalog.db 的唯一读写者，对内网暴露 /internal/*
// 管理接口；admin 仅作代理（requireRole("admin")）并转换响应信封，避免跨容器共享 SQLite 写。
//
// curation.json 例外：它是一个可手工编辑、提交进仓库的纯文本审校文件，由 admin 直接写入
// 媒体中心配置目录（media_server 以只读方式挂载读取），不经过 media_server 接口。

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
	return strings.TrimSpace(getEnv("MEDIA_SERVER_INTERNAL_TOKEN"))
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
// 成功时把上游 JSON 原样作为 data 透传;失败时提取上游消息用 writeError 返回对应状态码。
func proxyMediaCenterInternal(w http.ResponseWriter, ctx context.Context, method, path string, query url.Values, body []byte) {
	status, payload, err := callMediaCenterInternal(ctx, method, path, query, body)
	if err != nil {
		if errors.Is(err, errMediaCenterInternalDisabled) {
			writeError(w, "媒体中心内部管理接口未启用：请在 .env 配置 MEDIA_SERVER_INTERNAL_TOKEN 并重建 admin/media_server", http.StatusServiceUnavailable)
			return
		}
		writeInternalError(w, err)
		return
	}
	if status < 200 || status >= 300 {
		writeError(w, mediaCenterInternalErrorMessage(payload, status), status)
		return
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		writeJSON(w, struct{}{})
		return
	}
	if !json.Valid(payload) {
		writeInternalError(w, errors.New("media center internal API returned non-JSON body"))
		return
	}
	writeJSON(w, json.RawMessage(payload))
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
			writeError(w, "category 参数不能为空", http.StatusBadRequest)
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
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodGet, "/internal/catalog/items", q, nil)
	}
}

// apiMediaCatalogReview 代理 POST /internal/catalog/review：对单个成员 approve/reject/置顶/排序。
// admin 强制把 reviewer 覆盖为当前登录用户名（审计），忽略客户端自带的 reviewer 字段。
func apiMediaCatalogReview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			writeInternalError(w, err)
			return
		}
		var req map[string]json.RawMessage
		if err := json.Unmarshal(raw, &req); err != nil {
			writeError(w, "请求体不是合法 JSON", http.StatusBadRequest)
			return
		}
		_, username := getUserFromContext(r)
		if reviewer, mErr := json.Marshal(strings.TrimSpace(username)); mErr == nil {
			req["reviewer"] = reviewer
		}
		body, err := json.Marshal(req)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodPost, "/internal/catalog/review", nil, body)
	}
}

// apiMediaCatalogSeed 代理 POST /internal/catalog/seed：异步把实时聚合结果灌成 pending 候选。
func apiMediaCatalogSeed() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			writeInternalError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodPost, "/internal/catalog/seed", nil, bytes.TrimSpace(body))
	}
}

// apiMediaSnapshotsRebuild 代理 POST /internal/snapshots/rebuild：异步重建运行时快照。
func apiMediaSnapshotsRebuild() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			writeInternalError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), mediaCenterInternalTimeout)
		defer cancel()
		proxyMediaCenterInternal(w, ctx, http.MethodPost, "/internal/snapshots/rebuild", nil, bytes.TrimSpace(body))
	}
}

// mediaCurationCategory 镜像 media_server curation.go 的 rawCategoryCuration 结构。
type mediaCurationCategory struct {
	BlockIDs           []string `json:"blockIds"`
	BlockTitleContains []string `json:"blockTitleContains"`
	PinIDs             []string `json:"pinIds"`
}

// mediaCurationDoc 是本地审校文件 curation.json 的顶层结构。
type mediaCurationDoc struct {
	Categories map[string]mediaCurationCategory `json:"categories"`
}

func dedupTrimList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		t := strings.TrimSpace(v)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// normalizeMediaCuration 清洗审校文档：去空白/去重/丢弃完全为空的分类，保证写盘内容稳定。
func normalizeMediaCuration(in mediaCurationDoc) mediaCurationDoc {
	out := mediaCurationDoc{Categories: map[string]mediaCurationCategory{}}
	for id, cat := range in.Categories {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		norm := mediaCurationCategory{
			BlockIDs:           dedupTrimList(cat.BlockIDs),
			BlockTitleContains: dedupTrimList(cat.BlockTitleContains),
			PinIDs:             dedupTrimList(cat.PinIDs),
		}
		if len(norm.BlockIDs) == 0 && len(norm.BlockTitleContains) == 0 && len(norm.PinIDs) == 0 {
			continue
		}
		out.Categories[id] = norm
	}
	return out
}

func readMediaCuration() (mediaCurationDoc, error) {
	doc := mediaCurationDoc{Categories: map[string]mediaCurationCategory{}}
	data, err := os.ReadFile(mediaCenterCurationPath)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return mediaCurationDoc{Categories: map[string]mediaCurationCategory{}}, err
	}
	if doc.Categories == nil {
		doc.Categories = map[string]mediaCurationCategory{}
	}
	return doc, nil
}

func writeMediaCuration(doc mediaCurationDoc) error {
	if err := os.MkdirAll(filepath.Dir(mediaCenterCurationPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(mediaCenterCurationPath, data, 0644)
}

// ensureMediaCuration 在部署时保证 curation.json 存在（缺失则写入空文档），使 media_server
// 只读挂载的 /config 目录总有一个合法的审校文件。已存在则原样保留，不覆盖人工编辑。
func ensureMediaCuration() error {
	if _, err := os.Stat(mediaCenterCurationPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeMediaCuration(mediaCurationDoc{Categories: map[string]mediaCurationCategory{}})
}

// apiMediaCurationGet 读取本地审校文件 curation.json（缺失时返回空文档）。
func apiMediaCurationGet() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		doc, err := readMediaCuration()
		if err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"curation": doc,
			"file":     mediaCenterCurationPath,
		})
	}
}

// apiMediaCurationSave 校验并原子写入 curation.json。审校文件仅在“重建快照/实时聚合”时生效，
// 因此响应里提示需要随后触发一次快照重建。
func apiMediaCurationSave() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var incoming mediaCurationDoc
		if err := decodeStrictJSON(r.Body, &incoming); err != nil {
			writeError(w, "审校文件不是合法 JSON 或包含未知字段", http.StatusBadRequest)
			return
		}
		normalized := normalizeMediaCuration(incoming)
		if err := writeMediaCuration(normalized); err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"curation":      normalized,
			"file":          mediaCenterCurationPath,
			"rebuildNeeded": true,
		})
	}
}
