package transfer

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRangeCountsAsDownload(t *testing.T) {
	const size = 1_000_000
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"无 Range 完整下载", "", true},
		{"从 0 开始完整", "bytes=0-", true},
		{"从 0 开始封闭到末尾", "bytes=0-999999", true},
		{"首段分段(多连接下载器第一块)", "bytes=0-333333", false},
		{"中间分段(续传)", "bytes=333334-666667", false},
		{"中间开放范围(未到末尾? 开放范围必达末尾)", "bytes=1-", true},
		{"后缀范围取全文", "bytes=-1000000", true},
		{"后缀范围取尾部", "bytes=-1024", true},
		{"末段分段", "bytes=666667-999999", true},
		{"大写 Bytes=", "BYTES=0-", true},
		{"多段范围", "bytes=0-1,5-10", true},
		{"非法头", "bytes=abc", true},
		{"缺前缀", "chunk=0-", true},
		{"越界起始", "bytes=2000000-", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rangeCountsAsDownload(c.header, size); got != c.want {
				t.Errorf("rangeCountsAsDownload(%q, %d) = %v, want %v", c.header, size, got, c.want)
			}
		})
	}
}

func TestValidTransferFilename(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"song.mp3", true},
		{"我的歌曲 - 版本2.flac", true},
		{"带空格 与 点.的名字.wav", true},
		{"a/b.mp3", false},
		{"a\\b.mp3", false},
		{"..", false},
		{".", false},
		{"带控制字符\x01.mp3", false},
		{"带 DEL \x7f.mp3", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validTransferFilename(c.name); got != c.want {
				t.Errorf("validTransferFilename(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
	long := strings.Repeat("中", 86) + ".mp3" // 258 字节
	if validTransferFilename(long) {
		t.Errorf("超过 255 字节的文件名应被拒绝")
	}
}

func TestTransferContentDispositionUnicode(t *testing.T) {
	// 非 ASCII 文件名必须走 RFC 5987 编码(filename*=),浏览器才能正确保存。
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": "我的歌.mp3"})
	if !strings.Contains(disposition, "filename*") {
		t.Fatalf("非 ASCII 文件名应产生 filename* 编码, got %q", disposition)
	}
	if disposition == "" {
		t.Fatal("FormatMediaType 不应返回空串")
	}
}

// TestDeliveredFullDownload 锁定下载计数的"实际交付"判定:HEAD、416、
// 中途断开、不覆盖末字节的中间分段都不消耗 MaxDownloads;完整 200、
// 覆盖末字节的 206 分段计数。
func TestDeliveredFullDownload(t *testing.T) {
	size := int64(100)

	cw := func(status int, written int64, writeErr bool) *countingResponseWriter {
		return &countingResponseWriter{status: status, written: written, writeErr: writeErr}
	}
	req := func(method, rangeHeader string) *http.Request {
		r := httptest.NewRequest(method, "/api/transfer/abc12345", nil)
		if rangeHeader != "" {
			r.Header.Set("Range", rangeHeader)
		}
		return r
	}

	cases := []struct {
		name           string
		r              *http.Request
		cw             *countingResponseWriter
		specCoversLast bool
		want           bool
	}{
		{"HEAD 不计数", req(http.MethodHead, ""), cw(200, 0, false), true, false},
		{"200 完整交付计数", req(http.MethodGet, ""), cw(200, size, false), true, true},
		{"200 发送不全(断开)不计数", req(http.MethodGet, ""), cw(200, 40, true), true, false},
		{"416 越界 range 不计数", req(http.MethodGet, "bytes=999999999-"), cw(416, 0, false), true, false},
		{"中间分段不计数", req(http.MethodGet, "bytes=10-19"), cw(206, 10, false), false, false},
		{"覆盖末字节的分段计数", req(http.MethodGet, "bytes=50-"), cw(206, 50, false), true, true},
		{"后缀分段计数", req(http.MethodGet, "bytes=-30"), cw(206, 30, false), true, true},
		{"304 Not Modified 不计数", req(http.MethodGet, ""), cw(304, 0, false), true, false},
	}
	for _, tc := range cases {
		if got := deliveredFullDownload(tc.r, tc.cw, tc.specCoversLast, size); got != tc.want {
			t.Errorf("%s: delivered=%v want %v", tc.name, got, tc.want)
		}
	}
}

// TestValidTransferFilenameRejectsBidi 拒绝 Unicode 双向排版控制字符
// (视觉反转扩展名伪装),正常 Unicode 文件名不受影响。
func TestValidTransferFilenameRejectsBidi(t *testing.T) {
	rejected := []string{
		"exe.jar\u202Eexe", // RLO 反转显示
		"\u2066dir\u2069/x",
		"file\u202Dname.txt",
	}
	for _, name := range rejected {
		if validTransferFilename(name) {
			t.Errorf("bidi filename should be rejected: %q", name)
		}
	}
	accepted := []string{"文件 名.txt", "report-v1.pdf", "archivo año.csv"}
	for _, name := range accepted {
		if !validTransferFilename(name) {
			t.Errorf("normal unicode filename should pass: %q", name)
		}
	}
}
