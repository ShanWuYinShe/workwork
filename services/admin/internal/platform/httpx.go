package platform

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

type JSONResp struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	// ErrorCode 是机器可读错误码（失败时填充），供前端/fail2ban 等调用方
	// 按业务分支处理，而不是只靠解析 human-readable 文案。
	ErrorCode string `json:"error_code,omitempty"`
}

func WriteJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(JSONResp{Success: true, Data: data}); err != nil {
		log.Printf("WARNING: WriteJSON encode: %v", err)
	}
}

func WriteError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// error_code 全量化:调用方未给专用码时按 HTTP 状态派生默认码,保证
	// 前端 ApiError.code 恒非空(可按码分支/i18n),而不是只能解析文案。
	if err := json.NewEncoder(w).Encode(JSONResp{Success: false, Error: msg, ErrorCode: defaultErrorCode(code)}); err != nil {
		log.Printf("WARNING: WriteError encode: %v", err)
	}
}

// defaultErrorCode 按 HTTP 状态派生机器可读的默认错误码。
func defaultErrorCode(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "BAD_REQUEST"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "CONFLICT"
	case http.StatusGone:
		return "GONE"
	case http.StatusRequestTimeout:
		return "TIMEOUT"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	case http.StatusBadGateway:
		return "BAD_GATEWAY"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	default:
		return fmt.Sprintf("HTTP_%d", code)
	}
}

// WriteErrorCode 与 WriteError 行为一致，仅额外携带机器可读错误码。
// 独立函数而非改 WriteError 签名：现有调用点（~60 处）不受影响，
// 只有需要调用方分支处理的端点（如 security block/unblock）使用。
func WriteErrorCode(w http.ResponseWriter, msg string, code int, errCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(JSONResp{Success: false, Error: msg, ErrorCode: errCode}); err != nil {
		log.Printf("WARNING: WriteErrorCode encode: %v", err)
	}
}

// WriteInternalError returns a generic 500 to the client while logging the
// full error server-side. Use this for any error whose underlying message
// could leak internal state (file paths, docker container IDs, CF API
// responses, JSON struct field names, etc.).
func WriteInternalError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	WriteError(w, "内部错误", http.StatusInternalServerError)
}

func DecodeStrictJSON(r io.Reader, dest any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain only one JSON value")
		}
		return err
	}
	return nil
}

// staticAssetPrefixes / staticAssetFiles 是 SPA 静态资源清单的唯一来源：
// mux 注册、缓存策略、限流豁免都从这里派生，新增资源不需要改三处。
var staticAssetPrefixes = []string{"/assets/"}
var staticAssetFiles = []string{"/favicon.svg", "/icons.svg"}

// StaticAssetPatterns 返回 http.ServeMux 需要的静态资源模式。
func StaticAssetPatterns() []string {
	out := append([]string(nil), staticAssetPrefixes...)
	return append(out, staticAssetFiles...)
}

// IsStaticAsset 判断是否 admin SPA 静态资源路径(指纹文件名,可永久缓存
// 且跳过限流)。
func IsStaticAsset(path string) bool {
	for _, p := range staticAssetPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	for _, p := range staticAssetFiles {
		if path == p {
			return true
		}
	}
	return false
}

// sanitizeHeaderValue 移除 HTTP header 值中的危险字符
func SanitizeHeaderValue(s string) string {
	// 先转义反斜杠再转义双引号，否则第一步插入的 \ 会被第二步二次转义
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// ---- 全局请求体上限 ----

// BodyLimits 是不可变的请求体豁免集合。由组合根从路由表派生后注入，
// 不再有运行期可写的包级 map（此前 `ApplyBodyLimit` 在热路径无锁读取）。
type BodyLimits struct {
	exempt map[string]bool
}

func NewBodyLimits(exemptPaths []string) *BodyLimits {
	exempt := make(map[string]bool, len(exemptPaths))
	for _, path := range exemptPaths {
		exempt[path] = true
	}
	return &BodyLimits{exempt: exempt}
}

// Apply 对未豁免路径施加全局 1MB 请求体上限。
func (b *BodyLimits) Apply(w http.ResponseWriter, r *http.Request) {
	if b == nil || !b.exempt[r.URL.Path] {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB max request body
	}
}
