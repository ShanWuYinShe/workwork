package platform

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	// 使用唯一临时文件名，使同一目标路径的并发写入互不干扰。
	// 固定的 path+".tmp" 会让并发写入者争用同一 inode，导致内容损坏或 rename ENOENT。
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	// fchmod 不受 umask 影响，确保精确权限。
	if err := f.Chmod(perm); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	// fsync 数据落盘后再 rename:崩溃时要么旧文件完整、要么新文件完整,
	// 不会出现 rename 后数据丢失的窗口(users.json/manifest 等状态文件)。
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// LoadJSONFile 从 path 读取 JSON 文件到 dest。文件不存在时 dest 保持零值。
func LoadJSONFile(path string, dest any) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("ERROR: LoadJSONFile read %s: %v", path, err)
		}
		return
	}
	if err := json.Unmarshal(data, dest); err != nil {
		log.Printf("ERROR: LoadJSONFile decode %s: %v", path, err)
	}
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
