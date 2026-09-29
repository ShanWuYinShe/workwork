package platform

import (
	"context"
)

// 上传并发闸门:admin 容器内存上限 256M,免限上传端点仅剩 transfer
// (multipart 解析内存缓冲 32MB,transferUploadMemory)——无并发准入时约
// 8 个并发解析即逼近 cgroup 上限触发 OOM kill,面板整体中断一轮(进行中的
// 封禁同步/IP 轮换全部打断)。这里全局限 2 个并发解析位,多余请求排队且
// 客户端断开(ctx 取消)即自动放弃。2 位 = 最坏 64MB multipart 缓冲,叠加
// SSE/docker 流与 GC headroom 仍有余量。

const uploadSlots = 2

var uploadGate = make(chan struct{}, uploadSlots)

// AcquireUploadSlot 获取上传并发额度,返回释放函数。ctx 取消(客户端断开)
// 时放弃并返回 ok=false,调用方直接结束请求即可(响应无人接收)。
func AcquireUploadSlot(ctx context.Context) (release func(), ok bool) {
	select {
	case uploadGate <- struct{}{}:
		return func() { <-uploadGate }, true
	case <-ctx.Done():
		return nil, false
	}
}
