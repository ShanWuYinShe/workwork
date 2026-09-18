package platform

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
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
	// fsync 父目录把 rename 本身持久化:只 fsync 文件时,断电可能"新文件内容
	// 在而目录项丢失"。EnsureXrayClients 等以"文件是否存在"判断首启播种,
	// rename 未持久化 + 断电会让已删除/吊销的身份在下一次播种中复活。
	return syncDir(filepath.Dir(path))
}

// syncDir 打开目录并 fsync(rename 持久化的必要步骤)。
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// LoadJSONFile 从 path 读取 JSON 文件到 dest。文件不存在时 dest 保持零值。
// 解析失败时把损坏文件隔离为 <path>.corrupt 后再返回零值:调用方(manifest
// 类状态)会以零值继续并最终全量重写——若不隔离,下一次写入会把损坏的字节
// 静默覆盖掉,现场即丢失;隔离保留了原始字节供事后恢复,并触发 webhook 告警。
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
		quarantine := path + ".corrupt"
		if qerr := os.Rename(path, quarantine); qerr != nil {
			log.Printf("ERROR: LoadJSONFile quarantine %s: %v", quarantine, qerr)
		}
		NotifyWebhook("data.corrupt", "状态文件损坏已隔离: "+path, map[string]string{
			"path": path, "quarantined_as": quarantine,
		})
		return
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

// EnsurePerm 修正文件权限。内容不变但权限过期时（如从 0600 升级到 0644），
// 调用方自做的"内容未变化跳过写入"（writeMediaCenterCategoriesFileIfChanged
// 等）会让写入乃至 chmod 时机被跳过。此函数独立补上 chmod，与写入路径解耦。
func EnsurePerm(path string, want os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != want.Perm() {
		log.Printf("Fixing permissions: %s %o -> %o", path, info.Mode().Perm(), want.Perm())
		// os.Chmod 受 umask 影响，先用 syscall 确保精确权限
		return syscall.Chmod(path, uint32(want.Perm()))
	}
	return nil
}
