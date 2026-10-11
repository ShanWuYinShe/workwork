package proxy

import "testing"

// 覆盖 nginx access log 的归类：413 是"大分片被服务端拒绝"的唯一可观测
// 信号（xray 侧该日志为 Info 级，生产 loglevel=warning 不可见）。
func TestClassifyXHTTPPostLine(t *testing.T) {
	const prefix = `1.2.3.4 - - [11/Oct/2026:20:00:00 +0800] `
	cases := []struct {
		name  string
		line  string
		total int
		bad   int
		big   int
	}{
		{
			name:  "packet-up 正常 POST 只计总数",
			line:  prefix + `"POST /proxy/query/sess123/7 HTTP/1.1" 200 512 "-" "Mozilla/5.0" upstream: - uri="/proxy/query/sess123/7"`,
			total: 1,
		},
		{
			name:  "413 计入 tooLarge413",
			line:  prefix + `"POST /proxy/query/sess123/8 HTTP/1.1" 413 157 "-" "Mozilla/5.0" upstream: - uri="/proxy/query/sess123/8"`,
			total: 1,
			big:   1,
		},
		{
			name:  "400 计入 bad400",
			line:  prefix + `"POST /proxy/query/sess123/9 HTTP/1.1" 400 0 "-" "-" upstream: - uri="/proxy/query/sess123/9"`,
			total: 1,
			bad:   1,
		},
		{
			name: "非代理路径的 POST 忽略",
			line: prefix + `"POST /api/admin/users HTTP/1.1" 500 12 "-" "-" upstream: 172.18.0.4:8318 uri="/api/admin/users"`,
		},
		{
			name: "下行 stream-down 的 GET 忽略",
			line: prefix + `"GET /proxy/query HTTP/1.1" 200 4096 "-" "-" upstream: - uri="/proxy/query"`,
		},
		{
			name: "无状态码的行忽略",
			line: prefix + `"POST /proxy/query/sess123/1 HTTP/1.1"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stats xhttpPostStats
			classifyXHTTPPostLine(tc.line, &stats)
			if stats.total != tc.total || stats.bad400 != tc.bad || stats.tooLarge413 != tc.big {
				t.Fatalf("classify = %+v, want total=%d bad400=%d tooLarge413=%d", stats, tc.total, tc.bad, tc.big)
			}
		})
	}
}

func TestClassifyXHTTPPostLineAccumulates(t *testing.T) {
	const prefix = `1.2.3.4 - - [11/Oct/2026:20:00:00 +0800] `
	lines := []string{
		prefix + `"POST /proxy/query/s/1 HTTP/1.1" 413 157 "-" "-" uri="/proxy/query/s/1"`,
		prefix + `"POST /proxy/query/s/2 HTTP/1.1" 413 157 "-" "-" uri="/proxy/query/s/2"`,
		prefix + `"POST /proxy/query/s/3 HTTP/1.1" 200 512 "-" "-" uri="/proxy/query/s/3"`,
		prefix + `"GET /nginx-health HTTP/1.1" 204 0 "-" "-" uri="/nginx-health"`,
	}
	var stats xhttpPostStats
	for _, l := range lines {
		classifyXHTTPPostLine(l, &stats)
	}
	if stats.total != 3 || stats.tooLarge413 != 2 || stats.bad400 != 0 {
		t.Fatalf("unexpected accumulation: %+v", stats)
	}
}
