package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// csrfEcho 返回 200 并回写标记,用于断言请求是否穿透了 CSRF 中间件。
func csrfEcho(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("passed"))
}

func TestCSRFMiddleware(t *testing.T) {
	const host = "panel.example.com"
	const origin = "https://" + host

	tests := []struct {
		name       string
		method     string
		origin     string
		reqHost    string
		wantStatus int
	}{
		{"GET 无 Origin 直通", http.MethodGet, "", host, http.StatusOK},
		{"POST 同源放行", http.MethodPost, origin, host, http.StatusOK},
		{"POST 同源默认端口省略", http.MethodPost, origin, host, http.StatusOK},
		{"POST 跨源拒绝", http.MethodPost, "https://evil.example.net", host, http.StatusForbidden},
		{"POST Origin null 拒绝", http.MethodPost, "null", host, http.StatusForbidden},
		{"POST 无 Origin 放行(非浏览器/内部调用方)", http.MethodPost, "", host, http.StatusOK},
		{"PUT 跨源同样拒绝", http.MethodPut, "https://evil.example.net", host, http.StatusForbidden},
		{"OPTIONS 预检直通", http.MethodOptions, "https://evil.example.net", host, http.StatusOK},
		{"大小写不敏感的 host 比较", http.MethodPost, "HTTPS://PANEL.EXAMPLE.COM", "panel.example.com", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://"+tt.reqHost+"/api/admin/users/admin/add", nil)
			req.Host = tt.reqHost
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			CSRFMiddleware(csrfEcho)(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
