package transfer

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"math/big"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	transferDir          = "/app/data/transfer"
	transferManifestPath = "/app/data/transfer/manifest.json"
	transferMaxTotal     = 20 << 30 // 20GB
	transferMaxFile      = 2 << 30  // 2GB per file
	transferCodeLen      = 8
	transferDefaultExp   = 7 * 24 * time.Hour
	// transferDefaultPerUserQuota 是每用户默认上传配额;防止单个 viewer 用
	// 2GB×N 次上传填满全局 20GB。可用 env TRANSFER_USER_QUOTA 覆盖(0 = 不限)。
	transferDefaultPerUserQuota = 5 << 30 // 5GB
	// transferUploadMemory 是 multipart 解析的内存缓冲阈值；超出部分自动落盘为
	// 临时文件。单文件大小上限由 io.LimitReader + 截断检测强制执行，不能用这个值。
	transferUploadMemory = 32 << 20 // 32MB
)

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

var transferCodeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

type transferFile struct {
	Code         string `json:"code"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	ContentType  string `json:"content_type"`
	Uploader     string `json:"uploader"`
	UploadedAt   string `json:"uploaded_at"`
	ExpiresAt    string `json:"expires_at"`
	MaxDownloads int    `json:"max_downloads"` // 0 = unlimited
	Downloads    int    `json:"downloads"`
}

type transferManifest struct {
	Files     []transferFile `json:"files"`
	TotalSize int64          `json:"total_size"`
}

var (
	transferMu sync.Mutex
)

// validTransferFilename 校验上传文件名:拒绝路径分隔符与控制字符,其余
// (含中文等 Unicode)放行。落盘名是"分享码+扩展名"(transferFilePath),
// 原文件名只进 manifest 与下载响应头;响应头经 mime.FormatMediaType 做
// RFC 5987 编码,非 ASCII 与引号等特殊字符均被安全转义。
func validTransferFilename(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func loadTransferManifestLocked() transferManifest {
	var m transferManifest
	platform.LoadJSONFile(transferManifestPath, &m)
	return m
}

func saveTransferManifestLocked(m transferManifest) error {
	return platform.SaveJSONFile(transferManifestPath, m)
}

func generateTransferCode() (string, error) {
	chars := []byte(transferCodeChars)
	max := big.NewInt(int64(len(chars)))
	code := make([]byte, transferCodeLen)
	for i := range code {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		code[i] = chars[n.Int64()]
	}
	return string(code), nil
}

func generateUniqueTransferCode(m transferManifest) (string, error) {
	existing := map[string]bool{}
	for _, f := range m.Files {
		existing[f.Code] = true
	}
	for attempts := 0; attempts < 50; attempts++ {
		code, err := generateTransferCode()
		if err != nil {
			return "", err
		}
		if !existing[code] {
			return code, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique code after 50 attempts")
}

func transferFilePath(code, filename string) string {
	ext := filepath.Ext(filename)
	return filepath.Join(transferDir, code+ext)
}

// CleanupOrphanTempFiles 清理进程崩溃遗留的 multipart 上传临时文件
// （.upload-*.tmp）。正常路径会在请求结束时 RemoveAll，只有异常退出会残留。
func CleanupOrphanTempFiles() {
	entries, err := os.ReadDir(transferDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("WARNING: read transfer dir for orphan cleanup: %v", err)
		}
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".upload-") || !strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if err := os.Remove(filepath.Join(transferDir, e.Name())); err != nil {
			log.Printf("WARNING: remove orphan upload temp %s: %v", e.Name(), err)
		}
	}
}

// cleanupExpiredTransferFiles 删除过期或达下载上限的文件，返回释放的字节数
func cleanupExpiredTransferFiles() {
	transferMu.Lock()
	defer transferMu.Unlock()

	m := loadTransferManifestLocked()
	now := time.Now().UTC()
	var kept []transferFile
	var freed int64

	for _, f := range m.Files {
		expired := false
		if f.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, f.ExpiresAt); err == nil && now.After(t) {
				expired = true
			}
		}
		downloadsExhausted := f.MaxDownloads > 0 && f.Downloads >= f.MaxDownloads

		if expired || downloadsExhausted {
			os.Remove(transferFilePath(f.Code, f.Filename))
			freed += f.Size
		} else {
			kept = append(kept, f)
		}
	}

	removed := len(m.Files) - len(kept)
	if removed > 0 {
		m.Files = kept
		m.TotalSize -= freed
		if m.TotalSize < 0 {
			m.TotalSize = 0
		}
		if err := saveTransferManifestLocked(m); err != nil {
			log.Printf("ERROR: save transfer manifest (cleanup): %v", err)
		}
	}
}

// StartCleanupLoop 后台每小时清理过期文件
func StartCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cleanupExpiredTransferFiles()
		case <-ctx.Done():
			return
		}
	}
}

// POST /api/admin/transfer/upload — viewer+ 可上传
func ApiTransferUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, username := auth.GetUserFromContext(r)

		// 请求体硬上限（单文件 2GB + multipart 开销）。ParseMultipartForm 会把
		// 全部 file part 先无上限 spool 到磁盘才返回，header.Size 预检与配额
		// 检查都发生在其后——没有这层，超大请求体先把磁盘写满。nginx 侧的
		// client_max_body_size 只保护经 nginx 的路径，直连 admin 的内部调用
		// 不经过它。
		r.Body = http.MaxBytesReader(w, r.Body, transferMaxFile+64<<20)

		// 限制 multipart 内存缓冲为 32MB；更大的部分自动落盘为临时文件，
		// 避免单次上传占用 2GB 堆内存导致 OOM。单文件上限在下方强制执行。
		if err := r.ParseMultipartForm(transferUploadMemory); err != nil {
			platform.WriteError(w, "文件过大（最大 2GB）或请求格式错误", 400)
			return
		}
		// multipart 落盘的临时文件不会被自动清理，请求结束时显式删除。
		defer func() {
			if r.MultipartForm != nil {
				r.MultipartForm.RemoveAll()
			}
		}()

		file, header, err := r.FormFile("file")
		if err != nil {
			platform.WriteError(w, "未找到上传文件", 400)
			return
		}
		defer file.Close()

		// 校验文件名
		name := strings.TrimSpace(filepath.Base(header.Filename))
		if !validTransferFilename(name) {
			platform.WriteError(w, "文件名不合法（不允许路径分隔符或控制字符，最长 255 字符）", 400)
			return
		}

		// 单文件大小预检（header.Size 可靠时直接拒绝超大文件）
		if header.Size > transferMaxFile {
			platform.WriteError(w, fmt.Sprintf("文件超过 %dGB 上限", transferMaxFile>>30), 400)
			return
		}

		// 解析有效期
		expiresIn := r.FormValue("expires_in")
		duration := transferDefaultExp
		if expiresIn != "" {
			if d, err := time.ParseDuration(expiresIn); err == nil && d > 0 && d <= 365*24*time.Hour {
				duration = d
			}
		}

		// 解析最大下载次数
		maxDL := 0
		if v := r.FormValue("max_downloads"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				maxDL = n
			}
		}

		// Phase 0: 配额预检——在把整个文件写入磁盘之前,按 multipart 已知的
		// header.Size 快照检查全局/per-user 配额。没有这层,并发发起 N 个大上传
		// 可以先把磁盘塞满再在 Phase 2 逐一失败,配额形同虚设。Phase 2 仍保留
		// 基于实际写入量的精确检查(预检只是快速失败,不追求精确)。
		transferMu.Lock()
		snapshot := loadTransferManifestLocked()
		if snapshot.TotalSize+header.Size > transferMaxTotal {
			transferMu.Unlock()
			platform.WriteError(w, fmt.Sprintf("存储空间不足（已用 %s，上限 20GB）", formatBytes(snapshot.TotalSize)), 400)
			return
		}
		if quota := transferMaxPerUserBytes(); quota > 0 {
			var used int64
			for _, f := range snapshot.Files {
				if f.Uploader == username {
					used += f.Size
				}
			}
			if used+header.Size > quota {
				transferMu.Unlock()
				platform.WriteError(w, fmt.Sprintf("个人存储配额已用完（上限 %s）", formatBytes(quota)), 400)
				return
			}
		}
		transferMu.Unlock()

		// Phase 1: 文件写入磁盘（在锁外进行，避免阻塞其他 transfer 操作）
		if err := os.MkdirAll(transferDir, 0755); err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		// os.CreateTemp 用随机后缀,极端并发下不会像 UnixNano 那样 O_EXCL 碰撞。
		tmpFile, err := os.CreateTemp(transferDir, ".upload-*.tmp")
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		tmpPath := tmpFile.Name()
		dst := tmpFile

		written, err := io.Copy(dst, io.LimitReader(file, transferMaxFile))
		dst.Close()
		if err != nil {
			os.Remove(tmpPath)
			platform.WriteInternalError(w, err)
			return
		}

		// 截断检测：写入量达到上限时，尝试再读 1 字节判断是否还有数据
		if written >= transferMaxFile {
			var probe [1]byte
			n, _ := file.Read(probe[:])
			if n > 0 {
				os.Remove(tmpPath)
				platform.WriteError(w, fmt.Sprintf("文件超过 %dGB 上限", transferMaxFile>>30), 400)
				return
			}
		}

		// Phase 2: 加锁 — 用实际写入量做容量检查 + 生成分享码 + 重命名 + 更新 manifest
		transferMu.Lock()
		m := loadTransferManifestLocked()

		if m.TotalSize+written > transferMaxTotal {
			transferMu.Unlock()
			os.Remove(tmpPath)
			platform.WriteError(w, fmt.Sprintf("存储空间不足（已用 %s，上限 20GB）", formatBytes(m.TotalSize)), 400)
			return
		}
		// per-user 配额:防止单个 viewer 用 2GB×N 次上传填满全局配额。
		// 在锁内统计(manifest 一致视图)。
		if quota := transferMaxPerUserBytes(); quota > 0 {
			var used int64
			for _, f := range m.Files {
				if f.Uploader == username {
					used += f.Size
				}
			}
			if used+written > quota {
				transferMu.Unlock()
				os.Remove(tmpPath)
				platform.WriteError(w, fmt.Sprintf("个人存储配额已用完（上限 %s）", formatBytes(quota)), 400)
				return
			}
		}

		code, err := generateUniqueTransferCode(m)
		if err != nil {
			transferMu.Unlock()
			os.Remove(tmpPath)
			platform.WriteInternalError(w, err)
			return
		}

		dstPath := transferFilePath(code, name)
		if err := os.Rename(tmpPath, dstPath); err != nil {
			transferMu.Unlock()
			os.Remove(tmpPath)
			platform.WriteInternalError(w, err)
			return
		}

		// 更新 manifest
		now := time.Now().UTC()
		entry := transferFile{
			Code:         code,
			Filename:     name,
			Size:         written,
			ContentType:  header.Header.Get("Content-Type"),
			Uploader:     username,
			UploadedAt:   now.Format(time.RFC3339),
			ExpiresAt:    now.Add(duration).Format(time.RFC3339),
			MaxDownloads: maxDL,
			Downloads:    0,
		}
		m.Files = append(m.Files, entry)
		m.TotalSize += written
		saveErr := saveTransferManifestLocked(m)
		transferMu.Unlock()

		if saveErr != nil {
			log.Printf("ERROR: save transfer manifest (upload): %v", saveErr)
			// 磁盘文件已就位，但 manifest 未持久化 → 文件对管理面板不可见。
			// 不回滚已写入的磁盘文件（避免二次失败），如实上报。
			platform.WriteInternalError(w, fmt.Errorf("persist transfer manifest: %w", saveErr))
			return
		}

		log.Printf("[security] transfer upload code=%s file=%s size=%d by=%s", code, entry.Filename, entry.Size, username)
		platform.WriteJSON(w, map[string]any{
			"status": "ok",
			"code":   code,
			"file":   entry,
		})
	}
}

// GET /api/admin/transfer/files — viewer+ 查看列表
func ApiTransferList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := auth.GetUserFromContext(r)

		transferMu.Lock()
		m := loadTransferManifestLocked()
		transferMu.Unlock()

		// 权限过滤
		var files []transferFile
		if userInfo != nil && userInfo.Role == "admin" {
			files = m.Files
		} else {
			for _, f := range m.Files {
				if f.Uploader == username {
					files = append(files, f)
				}
			}
		}

		if files == nil {
			files = []transferFile{}
		}

		platform.WriteJSON(w, map[string]any{
			"files":      files,
			"total_size": m.TotalSize,
			"max_total":  transferMaxTotal,
		})
	}
}

// DELETE /api/admin/transfer/files — viewer+ 删除文件
func ApiTransferDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := auth.GetUserFromContext(r)

		var body struct {
			Code string `json:"code"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "Invalid JSON", 400)
			return
		}
		if body.Code == "" {
			platform.WriteError(w, "分享码必填", 400)
			return
		}

		transferMu.Lock()
		defer transferMu.Unlock()

		m := loadTransferManifestLocked()
		found := false
		var kept []transferFile
		for _, f := range m.Files {
			if f.Code == body.Code {
				// 权限检查：admin 可删任意文件，普通用户只能删自己的
				if userInfo != nil && userInfo.Role != "admin" && f.Uploader != username {
					platform.WriteError(w, "Forbidden: 不是你上传的文件", 403)
					return
				}
				os.Remove(transferFilePath(f.Code, f.Filename))
				m.TotalSize -= f.Size
				found = true
			} else {
				kept = append(kept, f)
			}
		}

		if !found {
			platform.WriteError(w, "文件不存在", 404)
			return
		}

		m.Files = kept
		if m.TotalSize < 0 {
			m.TotalSize = 0
		}
		if err := saveTransferManifestLocked(m); err != nil {
			log.Printf("ERROR: save transfer manifest (delete): %v", err)
			platform.WriteInternalError(w, fmt.Errorf("persist transfer manifest: %w", err))
			return
		}
		log.Printf("[security] transfer delete code=%s by=%s", body.Code, username)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

// GET /api/transfer/{code} — 公开下载，无需认证
func ApiTransferPublicDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if code == "" || len(code) != transferCodeLen {
			platform.WriteError(w, "无效的分享码", 400)
			return
		}
		// 只允许 alphanumeric
		for _, c := range code {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				platform.WriteError(w, "无效的分享码", 400)
				return
			}
		}

		transferMu.Lock()
		m := loadTransferManifestLocked()

		var target *transferFile
		for i := range m.Files {
			if m.Files[i].Code == code {
				target = &m.Files[i]
				break
			}
		}

		if target == nil {
			transferMu.Unlock()
			platform.WriteError(w, "文件不存在或已过期", 404)
			return
		}

		// 检查过期
		if target.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, target.ExpiresAt); err == nil && time.Now().UTC().After(t) {
				transferMu.Unlock()
				platform.WriteError(w, "分享链接已过期", 410)
				return
			}
		}

		// 检查下载次数
		if target.MaxDownloads > 0 && target.Downloads >= target.MaxDownloads {
			transferMu.Unlock()
			platform.WriteError(w, "下载次数已达上限", 410)
			return
		}

		// 计入下载计数：见 rangeCountsAsDownload——任何完整下载（无论是否分段）
		// 必然取到文件末字节，按"覆盖末字节才计数"每文件恰好计 1 次。
		if rangeCountsAsDownload(r.Header.Get("Range"), target.Size) {
			target.Downloads++
			if err := saveTransferManifestLocked(m); err != nil {
				// 下载计数持久化失败：不阻断下载本身（文件可读），仅记录。
				// 后续清理/列表会基于陈旧计数，可接受优于中断公开下载。
				log.Printf("ERROR: save transfer manifest (download count): %v", err)
			}
		}
		filePath := transferFilePath(code, target.Filename)
		// mime.FormatMediaType 生成 RFC 5987 编码(非 ASCII 文件名输出
		// filename*=utf-8''...,ASCII 输出普通 filename="..."),各浏览器
		// 都能以原文件名保存;非法输入返回空串时退化为纯 attachment。
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": target.Filename})
		if disposition == "" {
			disposition = "attachment"
		}

		// 在锁内打开文件获得 FD:ServeContent 基于已打开的 FD 提供内容,
		// 并发删除只移除目录项,FD 仍可完整读取——消除 unlock→ServeFile
		// 之间文件被删导致 404 的 TOCTOU 窗口。
		f, err := os.Open(filePath)
		if err != nil {
			transferMu.Unlock()
			if os.IsNotExist(err) {
				platform.WriteError(w, "文件不存在或已过期", 404)
				return
			}
			log.Printf("transfer download: open %s: %v", code, err)
			platform.WriteInternalError(w, err)
			return
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			transferMu.Unlock()
			log.Printf("transfer download: stat %s: %v", code, err)
			platform.WriteInternalError(w, err)
			return
		}
		transferMu.Unlock()
		defer f.Close()

		w.Header().Set("Content-Disposition", disposition)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, target.Filename, info.ModTime(), f)
	}
}

// rangeCountsAsDownload 判断一次（可能带 Range 的）请求是否计入下载次数。
//
// 任何完整下载——单连接无 Range、或多连接下载管理器的某个分段——都必然取到
// 文件的最后一个字节。因此只有"覆盖文件末字节"的请求计数：完整下载恰好计 1
// （取末字节的那个分段），中间续传分段（bytes=1048576-2097151）不计。这同时
// 堵住了两个绕过：后缀范围 bytes=-N（N≥文件大小即取到全文末字节，必须计数）
// 与 bytes=1-（跳过首字节的开放范围，覆盖末字节，必须计数）。任何解析不了或
// 越界的范围保守计数（fail-closed），交给 http.ServeContent 决定 416/正常响应。
func rangeCountsAsDownload(rangeHeader string, size int64) bool {
	if rangeHeader == "" {
		return true
	}
	trimmed := strings.TrimSpace(rangeHeader)
	lower := strings.ToLower(trimmed)
	spec := strings.TrimPrefix(lower, "bytes=")
	if spec == lower {
		return true // 缺 bytes= 前缀的非法头：保守计数
	}
	if strings.ContainsRune(spec, ',') {
		return true // 多段范围：保守计数
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return true // 非法格式：保守计数
	}
	start, end := strings.TrimSpace(spec[:dash]), strings.TrimSpace(spec[dash+1:])
	if start == "" {
		return true // 后缀范围 bytes=-N：覆盖末字节
	}
	s, err := strconv.ParseInt(start, 10, 64)
	if err != nil || s < 0 || s >= size {
		return true // 非法/越界起始：保守计数
	}
	if end == "" {
		return true // 开放范围 bytes=S-：覆盖末字节
	}
	e, err := strconv.ParseInt(end, 10, 64)
	if err != nil {
		return true // 非法结束值：保守计数
	}
	return e >= size-1 // 封闭分段：仅当覆盖末字节时计数
}

// formatBytes 格式化字节大小
func formatBytes(b int64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	}
	if b >= 1<<20 {
		return fmt.Sprintf("%.0f MB", float64(b)/float64(1<<20))
	}
	return fmt.Sprintf("%d B", b)
}
