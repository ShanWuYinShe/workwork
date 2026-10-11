package infra

import (
	"net/http"
	"strconv"

	"admin/internal/platform"
)

// speedtestMaxBytes 单次测速响应上限。32MB 按内网上行(约 160Mbps)不到 2 秒即可
// 测准带宽,同时压低该公开端点被滥用的带宽成本(全局限流 20req/10s 之外的第二道闸)。
const speedtestMaxBytes = 32 << 20

// speedtestChunk 响应填充块:进程内生成一次的伪随机内容。全零内容可能被中间层
// 优化/压缩导致测速失真,这里用 LCG 填充保证不可压缩;不引入 crypto/rand 的开销。
var speedtestChunk = func() []byte {
	b := make([]byte, 64<<10)
	x := uint32(0x9e3779b9)
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}()

// ApiSpeedtest 公开带宽/延迟测量端点(无认证):GET /speedtest/{size}。
// size 为响应字节数(0 = 仅响应头,用于 TTFB 延迟测量),上限 32MB。
// 用途:优选 CF 边缘 IP 的矩阵测试(curl --resolve 指定候选边缘直连本服务)
// 与用户自测本地到本服务的链路质量。IP 封禁、全局限流、CSRF 中间件均覆盖本路径。
// 只读端点,无审计留痕需求。
func ApiSpeedtest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		size, err := strconv.Atoi(r.PathValue("size"))
		if err != nil || size < 0 || size > speedtestMaxBytes {
			platform.WriteError(w, "size 必须是 0-"+strconv.Itoa(speedtestMaxBytes)+" 之间的整数", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.WriteHeader(http.StatusOK)
		for written := 0; written < size; {
			n := size - written
			if n > len(speedtestChunk) {
				n = len(speedtestChunk)
			}
			if _, err := w.Write(speedtestChunk[:n]); err != nil {
				return // 客户端提前断开是测速常态,静默结束
			}
			written += n
		}
	}
}
