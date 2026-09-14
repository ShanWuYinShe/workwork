package platform

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
