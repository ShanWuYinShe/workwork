package main

// ai_analyze.go：media_center 视频源 AI 辅助分析。
//
// 设计依据（media_server/AGENTS.md + CATEGORY_CURATION_GUIDE.md）：
//   - media_server 是纯目录服务，绝不调上游 API / 不做实时采集；它只接收已审核数据。
//   - 视频源"是否符合要求"的 AI 判定是离线/带外步骤——在"添加视频源时"由 admin 调用 LLM。
//   - AI 给出 verdict(pass/fail) + reason + score；拒绝项仍以 status=pending + reason
//     写入 catalog（category_members.reason 列），人工在审校页最终决定。
//     "审核是唯一质量关口"——AI 仅做建议标注，不自动上线/丢弃。
//
// 接入：标准 OpenAI Chat Completions 兼容 POST {base}/chat/completions，Bearer 鉴权。
// 兼容 OpenAI / DeepSeek / Kimi / Qwen / 本地 Ollama 等所有 OpenAI 格式端点。
// 配置：AI_BASE_URL / AI_API_KEY / AI_MODEL（任一为空则功能禁用，handler 返回明确错误）。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	aiAnalyzeMaxItems     = 500 // 单次分析上限（防止超大请求）
	aiAnalyzeBatchSize    = 20  // 单批送 LLM 的候选数（控制 token 与延迟）
	aiAnalyzeRequestLimit = 4 << 20
	aiAnalyzeLLMTimeout   = 90 * time.Second
	aiAnalyzeDefaultModel = "gpt-4o-mini"
	aiAnalyzeMaxRespBytes = 4 << 20
)

// aiAnalyzeCandidate 是送入分析的一条候选（来自导入信封，字段裁剪到分析所需）。
type aiAnalyzeCandidate struct {
	Index           int      `json:"index"` // 在原始 items 中的下标，用于结果对齐
	CategoryID      string   `json:"categoryId"`
	Provider        string   `json:"provider"`
	NativeID        string   `json:"nativeId"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Creators        []string `json:"creators,omitempty"`
	DurationSeconds int64    `json:"durationSeconds,omitempty"`
	Year            string   `json:"year,omitempty"`
}

// aiVerdict 是 LLM 对单条候选的判定。
type aiVerdict struct {
	Index   int    `json:"index"`
	Verdict string `json:"verdict"` // "pass" | "fail"
	Reason  string `json:"reason"`  // 中文理由
	Score   int    `json:"score"`   // 0-100
}

// aiAnalyzeConfig 从环境读取 LLM 接入配置。BaseURL/APIKey 任一为空 → 禁用。
type aiAnalyzeConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

func loadAIAnalyzeConfig() aiAnalyzeConfig {
	base := strings.TrimRight(strings.TrimSpace(getEnv("AI_BASE_URL")), "/")
	key := strings.TrimSpace(getEnv("AI_API_KEY"))
	model := strings.TrimSpace(getEnv("AI_MODEL"))
	if model == "" {
		model = aiAnalyzeDefaultModel
	}
	return aiAnalyzeConfig{BaseURL: base, APIKey: key, Model: model}
}

func (c aiAnalyzeConfig) enabled() bool {
	return c.BaseURL != "" && c.APIKey != ""
}

// errAIAnalyzeDisabled 未配置 AI_BASE_URL / AI_API_KEY 时返回的明确错误。
var errAIAnalyzeDisabled = errors.New("AI 分析未配置：请在 .env 设置 AI_BASE_URL 与 AI_API_KEY")

var aiAnalyzeClient = &http.Client{Timeout: aiAnalyzeLLMTimeout}

// analyzeSources 把候选按批次送 LLM，按 CATEGORY_CURATION_GUIDE 通用原则判定每条
// 是否"符合要求"。返回与输入下标对齐的 verdict 列表（顺序不保证，靠 index 对齐）。
// 失败时返回 error（调用方用 writeInternalError，不向客户端泄露内部细节）。
func analyzeSources(ctx context.Context, cfg aiAnalyzeConfig, categoriesByID map[string]aiCategoryBrief, items []aiAnalyzeCandidate) ([]aiVerdict, error) {
	if !cfg.enabled() {
		return nil, errAIAnalyzeDisabled
	}
	if len(items) == 0 {
		return nil, errors.New("没有可分析的候选")
	}
	results := make([]aiVerdict, 0, len(items))
	for start := 0; start < len(items); start += aiAnalyzeBatchSize {
		end := start + aiAnalyzeBatchSize
		if end > len(items) {
			end = len(items)
		}
		batch := items[start:end]
		verdicts, err := analyzeBatch(ctx, cfg, categoriesByID, batch)
		if err != nil {
			return nil, fmt.Errorf("analyze batch %d-%d: %w", start, end-1, err)
		}
		results = append(results, verdicts...)
	}
	return results, nil
}

// analyzeBatch 对一批（≤ aiAnalyzeBatchSize）候选调用一次 LLM。
func analyzeBatch(ctx context.Context, cfg aiAnalyzeConfig, categoriesByID map[string]aiCategoryBrief, batch []aiAnalyzeCandidate) ([]aiVerdict, error) {
	systemPrompt, userPrompt := buildAnalyzePrompt(categoriesByID, batch)
	resp, err := callLLM(ctx, cfg, systemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	return parseLLMVerdicts(resp, len(batch))
}

// aiCategoryBrief 是分类的精简描述（供 prompt 引用），由 media_server categories 派生。
type aiCategoryBrief struct {
	ID          string
	Title       string
	Description string
	Keywords    []string
}

// buildAnalyzePrompt 构造 system + user prompt。system 内嵌通用审核原则（与
// CATEGORY_CURATION_GUIDE.md 一致），user 给出本批分类上下文 + 候选清单，要求
// LLM 严格返回 JSON。
func buildAnalyzePrompt(categoriesByID map[string]aiCategoryBrief, batch []aiAnalyzeCandidate) (string, string) {
	system := `你是视频媒体中心的分类审核助手。判断每条候选视频是否适合收录到指定分类。
严格遵循以下通用审核原则（与站内 CATEGORY_CURATION_GUIDE.md 一致）：

1. 可播放性：付费墙、地区限制、已下架、需登录的条目判 fail。
2. 内容相符：实际内容须与标题、所属分类一致；标题党、名不符实判 fail。
3. 单一归属：只应归最契合的一个分类；明显属于其他分类的判 fail。
4. 形态合规（无合适分类即 fail）：
   - 游戏录屏 / 攻略 / 流程解说
   - 纯音频 / 音乐（无画面）
   - 短视频拼接 / 合集刷量
   - 广告 / 引流 / 推广
   - 影视解说 / 混剪 / "X 分钟看完"（非原片）
   - 动画 / 番剧（当前无动画分类）
5. 元数据：标题缺失或为乱码判 fail；description/duration 缺失不判 fail 但会降低 score。

只输出严格的 JSON 对象，不要 markdown 代码块、不要解释。
格式：{"results":[{"index":0,"verdict":"pass|fail","reason":"中文一句话理由","score":0到100的整数}, ...]}
必须为本批每条候选各给一条结果，index 与输入下标严格对应。`

	// 汇总本批涉及的分类上下文，让 LLM 知道每个分类的定位。
	seen := make(map[string]struct{})
	var cats []aiCategoryBrief
	for _, it := range batch {
		if _, ok := seen[it.CategoryID]; ok {
			continue
		}
		seen[it.CategoryID] = struct{}{}
		if c, ok := categoriesByID[it.CategoryID]; ok {
			cats = append(cats, c)
		}
	}
	var catBuf strings.Builder
	for _, c := range cats {
		fmt.Fprintf(&catBuf, "- %s（%s）", c.ID, c.Title)
		if c.Description != "" {
			fmt.Fprintf(&catBuf, "：%s", c.Description)
		}
		if len(c.Keywords) > 0 {
			fmt.Fprintf(&catBuf, " 关键词：%s", strings.Join(c.Keywords, "、"))
		}
		catBuf.WriteByte('\n')
	}

	candidatesJSON, _ := json.Marshal(batch)
	user := fmt.Sprintf(`本批候选所属分类：
%s
候选清单（index 为下标）：
%s

请逐条判定是否符合其目标分类的收录要求。`, catBuf.String(), string(candidatesJSON))
	return system, user
}

// chatMessage / chatRequest / chatResponse 是 OpenAI Chat Completions 兼容结构。
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// callLLM 调用 OpenAI 兼容的 /chat/completions，返回 message.content。
// 带 response_format=json_object（部分端点不支持时降级重试一次）。
func callLLM(ctx context.Context, cfg aiAnalyzeConfig, system, user string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model: cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0.2,
		ResponseFormat: &struct {
			Type string `json:"type"`
		}{Type: "json_object"},
	})
	if err != nil {
		return "", err
	}

	resp, err := doLLMRequest(ctx, cfg, body)
	if err != nil {
		return "", err
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", fmt.Errorf("LLM 返回错误：%s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("LLM 响应无 choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func doLLMRequest(ctx context.Context, cfg aiAnalyzeConfig, body []byte) (chatResponse, error) {
	var cr chatResponse
	target := cfg.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return cr, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := aiAnalyzeClient.Do(req)
	if err != nil {
		return cr, fmt.Errorf("LLM 请求失败: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, aiAnalyzeMaxRespBytes))
	if err != nil {
		return cr, fmt.Errorf("读取 LLM 响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 不把响应体原文返回给客户端（可能含上游细节），仅记状态码。
		return cr, fmt.Errorf("LLM HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(payload, &cr); err != nil {
		return cr, fmt.Errorf("解析 LLM 响应 JSON: %w", err)
	}
	return cr, nil
}

// parseLLMVerdicts 解析 LLM 返回的 {"results":[...]}。
// 校验 verdict 合法、index 在范围内、score 落在 0-100；缺失的 index 补 fail + 兜底理由。
func parseLLMVerdicts(content string, expect int) ([]aiVerdict, error) {
	content = strings.TrimSpace(content)
	// 兼容被包进 markdown 代码块或多余文本的情况：截取首个 { 到最后一个 }。
	if i := strings.Index(content, "{"); i >= 0 {
		if j := strings.LastIndex(content, "}"); j > i {
			content = content[i : j+1]
		}
	}
	var env struct {
		Results []aiVerdict `json:"results"`
	}
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		return nil, fmt.Errorf("解析 verdict JSON: %w", err)
	}
	out := make([]aiVerdict, 0, expect)
	got := make(map[int]aiVerdict, len(env.Results))
	for _, v := range env.Results {
		v.Verdict = strings.ToLower(strings.TrimSpace(v.Verdict))
		if v.Verdict != "pass" && v.Verdict != "fail" {
			v.Verdict = "fail"
			if v.Reason == "" {
				v.Reason = "AI 返回的 verdict 非法，按 fail 处理"
			}
		}
		if v.Score < 0 {
			v.Score = 0
		} else if v.Score > 100 {
			v.Score = 100
		}
		if _, dup := got[v.Index]; !dup {
			got[v.Index] = v
		}
	}
	for i := 0; i < expect; i++ {
		if v, ok := got[i]; ok {
			out = append(out, v)
		} else {
			// LLM 漏了这条 → 兜底 fail，让人工再看一眼。
			out = append(out, aiVerdict{Index: i, Verdict: "fail", Reason: "AI 未给出判定，建议人工复核", Score: 0})
		}
	}
	return out, nil
}
