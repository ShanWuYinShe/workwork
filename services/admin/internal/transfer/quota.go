package transfer

import (
	"admin/internal/platform"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

const (
	transferMaxTotal = 20 << 30 // 20GB
	transferMaxFile  = 2 << 30  // 2GB per file
	transferCodeLen  = 8
	// reservedTransferCode 与 manifest.json 文件名(无扩展名部分)重合,禁止用作
	// 分享码,防止上传文件落盘覆盖 transferManifestPath(见 generateUniqueTransferCode)。
	reservedTransferCode = "manifest"
	transferDefaultExp   = 7 * 24 * time.Hour
	// transferDefaultPerUserQuota 是每用户默认上传配额;防止单个 viewer 用
	// 2GB×N 次上传填满全局 20GB。可用 env TRANSFER_USER_QUOTA 覆盖(0 = 不限)。
	transferDefaultPerUserQuota = 5 << 30 // 5GB
	// transferUploadMemory 是 multipart 解析的内存缓冲阈值；超出部分自动落盘为
	// 临时文件。单文件大小上限由 io.LimitReader + 截断检测强制执行，不能用这个值。
	transferUploadMemory = 32 << 20 // 32MB
	// maxTransferFilesPerUser 是每用户 manifest 条目数上限。配额按字节计,
	// 0 字节文件不占配额——没有条目上限时,已认证用户可无限堆 0 字节条目,
	// manifest 全量读写随条目数线性恶化所有 transfer 操作,并消耗宿主 inode。
	maxTransferFilesPerUser = 1000
)

// countUploaderEntries 统计某用户在 manifest 中的条目数(条目数上限检查用)。
func countUploaderEntries(m transferManifest, uploader string) int {
	n := 0
	for _, f := range m.Files {
		if f.Uploader == uploader {
			n++
		}
	}
	return n
}

// transferMaxPerUserBytes 返回每用户上传配额(0 = 不限),从 TRANSFER_USER_QUOTA
// 读取,默认 [transferDefaultPerUserQuota]。
func transferMaxPerUserBytes() int64 {
	raw := strings.TrimSpace(platform.GetEnv("TRANSFER_USER_QUOTA"))
	if raw == "" {
		return transferDefaultPerUserQuota
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
		return n
	}
	log.Printf("WARNING: invalid TRANSFER_USER_QUOTA %q, using default", raw)
	return transferDefaultPerUserQuota
}

// checkUploadQuota 统一全局容量、个人配额与单用户文件数检查。
// 预检（header.Size）与精确检查（实际 written）共用，避免两段逻辑漂移。
// 返回空串表示通过；否则是可直接返回给客户端的文案。
func checkUploadQuota(m transferManifest, uploader string, size int64) string {
	if m.TotalSize+size > transferMaxTotal {
		return fmt.Sprintf("存储空间不足（已用 %s，上限 20GB）", formatBytes(m.TotalSize))
	}
	if quota := transferMaxPerUserBytes(); quota > 0 {
		var used int64
		for _, f := range m.Files {
			if f.Uploader == uploader {
				used += f.Size
			}
		}
		if used+size > quota {
			return fmt.Sprintf("个人存储配额已用完（上限 %s）", formatBytes(quota))
		}
	}
	if countUploaderEntries(m, uploader) >= maxTransferFilesPerUser {
		return fmt.Sprintf("文件数量已达上限（每人最多 %d 个）", maxTransferFilesPerUser)
	}
	return ""
}
