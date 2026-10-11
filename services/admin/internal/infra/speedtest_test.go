package infra

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApiSpeedtestServesExactBytes(t *testing.T) {
	h := ApiSpeedtest()
	req := httptest.NewRequest("GET", "/speedtest/100000", nil)
	req.SetPathValue("size", "100000")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Length"); got != "100000" {
		t.Fatalf("Content-Length = %q, want 100000", got)
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) != 100000 {
		t.Fatalf("body len = %d, want 100000", len(body))
	}
	if string(body[:10]) != string(speedtestChunk[:10]) {
		t.Fatal("body 开头与填充块不一致")
	}
	if string(body[64<<10:(64<<10)+10]) != string(speedtestChunk[:10]) {
		t.Fatal("body 64KB 回绕处与填充块不一致")
	}
}

func TestApiSpeedtestZeroSizeIsLatencyProbe(t *testing.T) {
	h := ApiSpeedtest()
	req := httptest.NewRequest("GET", "/speedtest/0", nil)
	req.SetPathValue("size", "0")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Length"); got != "0" {
		t.Fatalf("Content-Length = %q, want 0", got)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body len = %d, want 0", rec.Body.Len())
	}
}

func TestApiSpeedtestRejectsInvalidSize(t *testing.T) {
	h := ApiSpeedtest()
	for _, size := range []string{"abc", "-1", "33554433"} {
		req := httptest.NewRequest("GET", "/speedtest/"+size, nil)
		req.SetPathValue("size", size)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != 400 {
			t.Fatalf("size=%q status = %d, want 400", size, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "size") {
			t.Fatalf("size=%q 响应未包含错误说明: %q", size, rec.Body.String())
		}
	}
}
