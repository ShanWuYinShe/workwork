package platform

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// CleanupStaleTempFiles 启动时清理 root 树内 AtomicWriteFile 的临时文件残留。
// SIGKILL(磁盘满时 OOM、宿主重启、部署期 stop 超时被 docker 杀)会让"rename
// 前删除"的 defer 失效,残留 .<name>.tmp-* 无人回收。music/media 专属前缀已
// 随服务拆分移除:现存临时文件写入路径只有 AtomicWriteFile 模式;transfer 的
// .upload-*.tmp 与 downloads 的 .download-* 各有专门的启动清理
// (transfer.CleanupOrphanTempFiles / downloads.PurgeStaleDownloadTemps),
// 且分别以 "." 开头和不以 "." 开头,不匹配本函数的扫描模式。
// 尽力而为:删除失败只记日志,不影响启动。返回清理的文件数。
func CleanupStaleTempFiles(root string) int {
	removed := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 无权限/已消失的子树跳过
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !(strings.HasPrefix(name, ".") && strings.Contains(name, ".tmp-")) {
			return nil
		}
		if rmErr := os.Remove(path); rmErr != nil {
			log.Printf("WARNING: cleanup stale temp file %s: %v", path, rmErr)
			return nil
		}
		removed++
		return nil
	})
	if err != nil {
		log.Printf("WARNING: cleanup stale temp files under %s: %v", root, err)
	}
	if removed > 0 {
		log.Printf("cleaned %d stale temp files under %s", removed, root)
	}
	return removed
}
