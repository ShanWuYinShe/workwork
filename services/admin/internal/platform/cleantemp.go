package platform

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// tempFilePrefixes 是各写入路径 CreateTemp 使用的临时文件前缀。SIGKILL(磁盘
// 满时 OOM、宿主重启、部署期 stop 超时被 docker 杀)会让"rename 前删除"的
// defer 失效,残留文件无人回收;downloads/transfer 有各自的启动清理,music/APK/
// AtomicWriteFile 的残留此前没有任何清理路径。这里启动时全树扫一遍统一兜底。
// 命名上所有模式都以 "." 开头且带 "-" 分隔的随机后缀,不会误伤正常数据文件。
var tempFilePrefixes = []string{
	".music-upload-",             // music_library.go 一次性上传
	".music-merge-",              // music_library_chunk.go 分片合并
	".chunk-",                    // music_library_chunk.go 分片落盘
	".media-center-app.apk.tmp-", // media_app_release.go APK 上传
}

// CleanupStaleTempFiles 启动时清理 root 树内已知临时文件残留
// (上方 tempFilePrefixes 的 CreateTemp 模式 + AtomicWriteFile 的
// .<name>.tmp-* 模式)。注意 transfer 的 .upload-*.tmp 与 downloads 的
// .download-* 不在清单内——它们各有专门的启动清理
// (transfer.CleanupOrphanTempFiles / downloads.PurgeStaleDownloadTemps),
// 两者分别以 "." 开头和不以 "." 开头,不匹配本函数的扫描模式。
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
		stale := strings.HasPrefix(name, ".") && strings.Contains(name, ".tmp-")
		if !stale {
			for _, p := range tempFilePrefixes {
				if strings.HasPrefix(name, p) {
					stale = true
					break
				}
			}
		}
		if !stale {
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
