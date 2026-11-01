package platform

import (
	"admin/internal/runtime"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
// 文件不存在返回 nil（dest 保持零值）；其他读错误返回 error；解析失败把
// 损坏文件隔离为 <path>.corrupt、外发告警并返回 ErrCorruptJSON 包装错误。
//
// 调用方禁止把 error 当"空表"继续：manifest/封禁表读失败后会被零值覆盖，
// 既有条目/封禁一次性丢失（数据丢失与 fail-open）。
func LoadJSONFile(path string, dest any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		log.Printf("ERROR: LoadJSONFile read %s: %v", path, err)
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		log.Printf("ERROR: LoadJSONFile decode %s: %v", path, err)
		quarantine := path + ".corrupt"
		if qerr := os.Rename(path, quarantine); qerr != nil {
			log.Printf("ERROR: LoadJSONFile quarantine %s: %v", quarantine, qerr)
		}
		NotifyWebhook("data.corrupt", "状态文件损坏已隔离: "+path, map[string]string{
			"path": path, "quarantined_as": quarantine,
		})
		return fmt.Errorf("%w: %s: %v", ErrCorruptJSON, path, err)
	}
	return nil
}

// SaveJSONFile 将 v 序列化为 JSON 并原子写入 path（自动创建目录）。
// 返回错误而非静默吞掉——manifest/磁盘一致性依赖调用方检查返回值并在失败时
// 中止（WriteInternalError），避免向用户上报成功但实际落盘失败导致状态漂移。
func SaveJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := AtomicWriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
