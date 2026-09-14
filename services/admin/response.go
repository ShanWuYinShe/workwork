package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

type jsonResp struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	// ErrorCode 是机器可读错误码（失败时填充），供前端/fail2ban 等调用方
	// 按业务分支处理，而不是只靠解析 human-readable 文案。
	ErrorCode string `json:"error_code,omitempty"`
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(jsonResp{Success: true, Data: data}); err != nil {
		log.Printf("WARNING: writeJSON encode: %v", err)
	}
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(jsonResp{Success: false, Error: msg}); err != nil {
		log.Printf("WARNING: writeError encode: %v", err)
	}
}

// writeErrorCode 与 writeError 行为一致，仅额外携带机器可读错误码。
// 独立函数而非改 writeError 签名：现有调用点（~60 处）不受影响，
// 只有需要调用方分支处理的端点（如 security block/unblock）使用。
func writeErrorCode(w http.ResponseWriter, msg string, code int, errCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(jsonResp{Success: false, Error: msg, ErrorCode: errCode}); err != nil {
		log.Printf("WARNING: writeErrorCode encode: %v", err)
	}
}

// security block API 业务错误码：前端 Security 页与 fail2ban 按此分支。
const (
	errCodeInvalidIP       = "INVALID_IP"
	errCodeInvalidJSON     = "INVALID_JSON"
	errCodeInvalidDuration = "INVALID_DURATION"
	errCodeBlockNotFound   = "BLOCK_NOT_FOUND"
)

// writeInternalError returns a generic 500 to the client while logging the
// full error server-side. Use this for any error whose underlying message
// could leak internal state (file paths, docker container IDs, CF API
// responses, JSON struct field names, etc.).
func writeInternalError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeError(w, "Internal error", http.StatusInternalServerError)
}

func decodeStrictJSON(r io.Reader, dest any) error {
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
