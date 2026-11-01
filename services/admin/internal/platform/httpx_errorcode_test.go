package platform

import (
	"net/http"
	"testing"
)

// TestDefaultErrorCode 锁定 error_code 的默认映射。前端 ApiError.code 依赖
// 它恒非空；新增/改状态码时这里必须同步，否则前端分支会静默落到 unknown。
func TestDefaultErrorCode(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:          "BAD_REQUEST",
		http.StatusUnauthorized:        "UNAUTHORIZED",
		http.StatusForbidden:           "FORBIDDEN",
		http.StatusNotFound:            "NOT_FOUND",
		http.StatusConflict:            "CONFLICT",
		http.StatusGone:                "GONE",
		http.StatusRequestTimeout:      "TIMEOUT",
		http.StatusTooManyRequests:     "RATE_LIMITED",
		http.StatusInternalServerError: "INTERNAL",
		http.StatusBadGateway:          "BAD_GATEWAY",
		http.StatusServiceUnavailable:  "UNAVAILABLE",
		http.StatusMethodNotAllowed:    "HTTP_405",
		http.StatusTeapot:              "HTTP_418",
	}
	for code, want := range cases {
		if got := defaultErrorCode(code); got != want {
			t.Errorf("defaultErrorCode(%d) = %q, want %q", code, got, want)
		}
	}
}
