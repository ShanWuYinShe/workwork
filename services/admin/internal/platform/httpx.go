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
	if err := json.NewEncoder(w).Encode(JSONResp{Success: false, Error: msg}); err != nil {
		log.Printf("WARNING: WriteError encode: %v", err)
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
	WriteError(w, "Internal error", http.StatusInternalServerError)
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

// IsStaticAsset 判断是否 admin SPA 静态资源路径(跳过限流/限流豁免用)。
// isStaticAsset returns true for fingerprinted asset paths that bypass rate limiting.
func IsStaticAsset(path string) bool {
	return len(path) >= 8 &&
		(path == "/favicon.svg" || path == "/icons.svg" ||
			(len(path) > 8 && path[:8] == "/assets/"))
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

// bodyLimitExemptPaths 由 main 在路由注册处对大上传端点调用 ExemptBodyLimit
// 声明(与路由声明同处,杜绝"加路由忘豁免"的双清单漂移);各自 handler 内
// 用 MaxBytesReader/io.LimitReader 施加自己的上限。
var bodyLimitExemptPaths = map[string]bool{}

// ExemptBodyLimit 登记大上传端点,豁免全局 1MB 请求体上限。
func ExemptBodyLimit(path string) { bodyLimitExemptPaths[path] = true }

// BodyLimitExempt 报告路径是否已豁免(供回归测试断言注册契约)。
func BodyLimitExempt(path string) bool { return bodyLimitExemptPaths[path] }

// ApplyBodyLimit 对未豁免路径施加全局 1MB 请求体上限。
func ApplyBodyLimit(w http.ResponseWriter, r *http.Request) {
	if !BodyLimitExempt(r.URL.Path) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB max request body
	}
}
