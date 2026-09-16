package auth

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 认证成功（含 RequireRole 之后的任何响应）的请求日志必须带 user 字段——
// 审计链路：RequireRole markAuditUser → RequestLogMiddleware 读取并输出。
func TestRequestLogRecordsAuditUser(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := RequestLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		markAuditUser(r.Context(), "alice")
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://admin/api/admin/dashboard", nil))

	if !strings.Contains(buf.String(), `user=alice`) {
		t.Fatalf("expected audit user in request log, got: %s", buf.String())
	}
}

// 未经过认证的请求（无 markAuditUser）不得输出 user 字段。
func TestRequestLogOmitsUserWhenUnauthenticated(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := RequestLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://admin/api/admin/dashboard", nil))

	if strings.Contains(buf.String(), "user=") {
		t.Fatalf("unauthenticated request must not carry user field, got: %s", buf.String())
	}
}
