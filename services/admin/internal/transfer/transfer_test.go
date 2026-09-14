package transfer

import "testing"

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
