package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockLLM 启动一个返回固定 chat completions 响应的 httptest.Server，
// 返回 server 与收到请求的捕获函数。
func mockLLM(t *testing.T, content string) (*httptest.Server, *string) {
	t.Helper()
	var lastBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux), &lastBody
}

func TestParseLLMVerdicts(t *testing.T) {
	content := `{"results":[
		{"index":0,"verdict":"pass","reason":"符合电影分类","score":90},
		{"index":1,"verdict":"fail","reason":"游戏录屏","score":20}
	]}`
	out, err := parseLLMVerdicts(content, 2)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 verdicts, got %d", len(out))
	}
	if out[0].Verdict != "pass" || out[0].Score != 90 {
		t.Errorf("verdict[0] = %+v", out[0])
	}
	if out[1].Verdict != "fail" || out[1].Reason != "游戏录屏" {
		t.Errorf("verdict[1] = %+v", out[1])
	}
}

func TestParseLLMVerdictsMissingIndexGetsFailFallback(t *testing.T) {
	// LLM 只回了 index 0，漏了 index 1 → 应补 fail 兜底。
	content := `{"results":[{"index":0,"verdict":"pass","reason":"ok","score":80}]}`
	out, err := parseLLMVerdicts(content, 2)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) != 2 || out[1].Verdict != "fail" {
		t.Fatalf("missing index should fall back to fail, got %+v", out)
	}
	if !strings.Contains(out[1].Reason, "人工复核") {
		t.Errorf("fallback reason = %q", out[1].Reason)
	}
}

func TestParseLLMVerdictsClampsScore(t *testing.T) {
	content := `{"results":[{"index":0,"verdict":"pass","reason":"ok","score":150}]}`
	out, _ := parseLLMVerdicts(content, 1)
	if out[0].Score != 100 {
		t.Errorf("score should clamp to 100, got %d", out[0].Score)
	}
}

func TestParseLLMVerdictsStripsMarkdownFence(t *testing.T) {
	content := "```json\n" + `{"results":[{"index":0,"verdict":"pass","reason":"ok","score":50}]}` + "\n```"
	out, err := parseLLMVerdicts(content, 1)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) != 1 || out[0].Verdict != "pass" {
		t.Fatalf("should parse fenced JSON, got %+v err=%v", out, err)
	}
}

func TestAnalyzeSourcesDisabledByDefault(t *testing.T) {
	// 无 BaseURL/APIKey → 明确禁用错误。
	cfg := aiAnalyzeConfig{}
	_, err := analyzeSources(context.Background(), cfg, nil, []aiAnalyzeCandidate{{Index: 0}})
	if err != errAIAnalyzeDisabled {
		t.Fatalf("want errAIAnalyzeDisabled, got %v", err)
	}
}

func TestAnalyzeSourcesEndToEnd(t *testing.T) {
	llmContent := `{"results":[
		{"index":0,"verdict":"pass","reason":"电影原片","score":88},
		{"index":1,"verdict":"fail","reason":"影视解说混剪","score":15}
	]}`
	srv, lastBody := mockLLM(t, llmContent)
	defer srv.Close()

	cfg := aiAnalyzeConfig{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"}
	items := []aiAnalyzeCandidate{
		{Index: 0, CategoryID: "movies", Provider: "bilibili", NativeID: "BV1", Title: "霸王别姬"},
		{Index: 1, CategoryID: "movies", Provider: "bilibili", NativeID: "BV2", Title: "5分钟看完霸王别姬"},
	}
	briefs := map[string]aiCategoryBrief{
		"movies": {ID: "movies", Title: "电影", Description: "华语电影", Keywords: []string{"电影"}},
	}
	out, err := analyzeSources(context.Background(), cfg, briefs, items)
	if err != nil {
		t.Fatalf("analyzeSources: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 results, got %d", len(out))
	}
	if out[0].Verdict != "pass" || out[1].Verdict != "fail" {
		t.Errorf("verdicts = %+v", out)
	}
	// 验证 prompt 含分类上下文与候选清单。
	if lastBody == nil || !strings.Contains(*lastBody, "movies") || !strings.Contains(*lastBody, "霸王别姬") {
		t.Errorf("request body missing context: %q", *lastBody)
	}
	// 验证 model 透传。
	if !strings.Contains(*lastBody, `"model":"test-model"`) {
		t.Errorf("model not in body: %s", *lastBody)
	}
	// 验证 Bearer 鉴权由 server 检查（server 在缺/错 token 时返回 401，callLLM 会报 HTTP 401）。
}

func TestAnalyzeSourcesBatching(t *testing.T) {
	// 25 条候选，batch=20 → 应触发 2 次 LLM 请求。
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		calls++
		// 简单回全 pass。
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": `{"results":[{"index":0,"verdict":"pass","reason":"ok","score":50}]}`}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfg := aiAnalyzeConfig{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	items := make([]aiAnalyzeCandidate, 25)
	for i := range items {
		items[i] = aiAnalyzeCandidate{Index: i, CategoryID: "movies", Provider: "bilibili", NativeID: "BV", Title: "T"}
	}
	out, err := analyzeSources(context.Background(), cfg, map[string]aiCategoryBrief{"movies": {ID: "movies", Title: "电影"}}, items)
	if err != nil {
		t.Fatalf("analyzeSources: %v", err)
	}
	if calls != 2 {
		t.Errorf("want 2 LLM calls for 25 items (batch 20), got %d", calls)
	}
	if len(out) != 25 {
		t.Errorf("want 25 verdicts, got %d", len(out))
	}
}
