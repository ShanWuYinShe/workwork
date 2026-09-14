package transfer

import (
	"mime"
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
