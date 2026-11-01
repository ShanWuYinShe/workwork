package platform

import (
	"admin/internal/runtime"
	"os"
)

// AtomicWriteFile 的实现在 runtime（唯一实现）；这里保留薄转发，避免
// 一次性改动全部调用点。新代码应直接使用 runtime.AtomicWriteFile。
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	return runtime.AtomicWriteFile(path, data, perm)
}

// EnsurePerm 的实现在 runtime。
func EnsurePerm(path string, want os.FileMode) error {
	return runtime.EnsurePerm(path, want)
}

// ErrCorruptJSON 与 runtime.ErrCorrupt 同源，保证调用方用 errors.Is 判断
// 时不会因为两套哨兵而漏掉。
var ErrCorruptJSON = runtime.ErrCorrupt

// LoadJSONFile 从 path 读取 JSON 文件到 dest。
// 唯一实现在 runtime.LoadJSON；此处仅绑定生产告警出口，供历史调用点使用。
func LoadJSONFile(path string, dest any) error {
	return runtime.LoadJSON(path, dest, runtime.NotifyFunc(NotifyWebhook))
}

// SaveJSONFile 将 v 序列化为 JSON 并原子写入 path（自动创建目录）。
// 唯一实现在 runtime.SaveJSON。
func SaveJSONFile(path string, v any) error {
	return runtime.SaveJSON(path, v, 0600)
}
