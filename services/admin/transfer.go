package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	raw := strings.TrimSpace(getEnv("TRANSFER_USER_QUOTA"))
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

// validTransferFilename 只允许安全的文件名字符，禁止路径穿越
var validTransferFilename = regexp.MustCompile(`^[a-zA-Z0-9_\-. ]+$`)

func loadTransferManifestLocked() transferManifest {
	var m transferManifest
	loadJSONFile(transferManifestPath, &m)
	return m
}

func saveTransferManifestLocked(m transferManifest) error {
	return saveJSONFile(transferManifestPath, m)
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

// cleanupOrphanTransferTempFiles 清理进程崩溃遗留的 multipart 上传临时文件
// （.upload-*.tmp）。正常路径会在请求结束时 RemoveAll，只有异常退出会残留。
func cleanupOrphanTransferTempFiles() {
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

// transferCleanupLoop 后台每小时清理过期文件
func transferCleanupLoop(ctx context.Context) {
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
func apiTransferUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, username := getUserFromContext(r)

		// 限制 multipart 内存缓冲为 32MB；更大的部分自动落盘为临时文件，
		// 避免单次上传占用 2GB 堆内存导致 OOM。单文件上限在下方强制执行。
		if err := r.ParseMultipartForm(transferUploadMemory); err != nil {
			writeError(w, "文件过大（最大 2GB）或请求格式错误", 400)
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
			writeError(w, "未找到上传文件", 400)
			return
		}
		defer file.Close()

		// 校验文件名
		name := filepath.Base(header.Filename)
		if !validTransferFilename.MatchString(name) || len(name) > 255 {
			writeError(w, "文件名不合法（仅允许字母数字、下划线、连字符、点、空格，最长 255 字符）", 400)
			return
		}

		// 单文件大小预检（header.Size 可靠时直接拒绝超大文件）
		if header.Size > transferMaxFile {
			writeError(w, fmt.Sprintf("文件超过 %dGB 上限", transferMaxFile>>30), 400)
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
			writeError(w, fmt.Sprintf("存储空间不足（已用 %s，上限 20GB）", formatBytes(snapshot.TotalSize)), 400)
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
				writeError(w, fmt.Sprintf("个人存储配额已用完（上限 %s）", formatBytes(quota)), 400)
				return
			}
		}
		transferMu.Unlock()

		// Phase 1: 文件写入磁盘（在锁外进行，避免阻塞其他 transfer 操作）
		if err := os.MkdirAll(transferDir, 0755); err != nil {
			writeInternalError(w, err)
			return
		}
		// os.CreateTemp 用随机后缀,极端并发下不会像 UnixNano 那样 O_EXCL 碰撞。
		tmpFile, err := os.CreateTemp(transferDir, ".upload-*.tmp")
		if err != nil {
			writeInternalError(w, err)
			return
		}
		tmpPath := tmpFile.Name()
		dst := tmpFile

		written, err := io.Copy(dst, io.LimitReader(file, transferMaxFile))
		dst.Close()
		if err != nil {
			os.Remove(tmpPath)
			writeInternalError(w, err)
			return
		}

		// 截断检测：写入量达到上限时，尝试再读 1 字节判断是否还有数据
		if written >= transferMaxFile {
			var probe [1]byte
			n, _ := file.Read(probe[:])
			if n > 0 {
				os.Remove(tmpPath)
				writeError(w, fmt.Sprintf("文件超过 %dGB 上限", transferMaxFile>>30), 400)
				return
			}
		}

		// Phase 2: 加锁 — 用实际写入量做容量检查 + 生成分享码 + 重命名 + 更新 manifest
		transferMu.Lock()
		m := loadTransferManifestLocked()

		if m.TotalSize+written > transferMaxTotal {
			transferMu.Unlock()
			os.Remove(tmpPath)
			writeError(w, fmt.Sprintf("存储空间不足（已用 %s，上限 20GB）", formatBytes(m.TotalSize)), 400)
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
				writeError(w, fmt.Sprintf("个人存储配额已用完（上限 %s）", formatBytes(quota)), 400)
				return
			}
		}

		code, err := generateUniqueTransferCode(m)
		if err != nil {
			transferMu.Unlock()
			os.Remove(tmpPath)
			writeInternalError(w, err)
			return
		}

		dstPath := transferFilePath(code, name)
		if err := os.Rename(tmpPath, dstPath); err != nil {
			transferMu.Unlock()
			os.Remove(tmpPath)
			writeInternalError(w, err)
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
			writeInternalError(w, fmt.Errorf("persist transfer manifest: %w", saveErr))
			return
		}

		writeJSON(w, map[string]any{
			"status": "ok",
			"code":   code,
			"file":   entry,
		})
	}
}

// GET /api/admin/transfer/files — viewer+ 查看列表
func apiTransferList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

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

		writeJSON(w, map[string]any{
			"files":      files,
			"total_size": m.TotalSize,
			"max_total":  transferMaxTotal,
		})
	}
}

// DELETE /api/admin/transfer/files — viewer+ 删除文件
func apiTransferDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

		var body struct {
			Code string `json:"code"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.Code == "" {
			writeError(w, "分享码必填", 400)
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
					writeError(w, "Forbidden: 不是你上传的文件", 403)
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
			writeError(w, "文件不存在", 404)
			return
		}

		m.Files = kept
		if m.TotalSize < 0 {
			m.TotalSize = 0
		}
		if err := saveTransferManifestLocked(m); err != nil {
			log.Printf("ERROR: save transfer manifest (delete): %v", err)
			writeInternalError(w, fmt.Errorf("persist transfer manifest: %w", err))
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

// GET /api/transfer/{code} — 公开下载，无需认证
func apiTransferPublicDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if code == "" || len(code) != transferCodeLen {
			writeError(w, "无效的分享码", 400)
			return
		}
		// 只允许 alphanumeric
		for _, c := range code {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				writeError(w, "无效的分享码", 400)
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
			writeError(w, "文件不存在或已过期", 404)
			return
		}

		// 检查过期
		if target.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, target.ExpiresAt); err == nil && time.Now().UTC().After(t) {
				transferMu.Unlock()
				writeError(w, "分享链接已过期", 410)
				return
			}
		}

		// 检查下载次数
		if target.MaxDownloads > 0 && target.Downloads >= target.MaxDownloads {
			transferMu.Unlock()
			writeError(w, "下载次数已达上限", 410)
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
		filename := sanitizeHeaderValue(target.Filename)

		// 在锁内打开文件获得 FD:ServeContent 基于已打开的 FD 提供内容,
		// 并发删除只移除目录项,FD 仍可完整读取——消除 unlock→ServeFile
		// 之间文件被删导致 404 的 TOCTOU 窗口。
		f, err := os.Open(filePath)
		if err != nil {
			transferMu.Unlock()
			if os.IsNotExist(err) {
				writeError(w, "文件不存在或已过期", 404)
				return
			}
			log.Printf("transfer download: open %s: %v", code, err)
			writeInternalError(w, err)
			return
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			transferMu.Unlock()
			log.Printf("transfer download: stat %s: %v", code, err)
			writeInternalError(w, err)
			return
		}
		transferMu.Unlock()
		defer f.Close()

		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, filename, info.ModTime(), f)
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

// sanitizeHeaderValue 移除 HTTP header 值中的危险字符
func sanitizeHeaderValue(s string) string {
	// 先转义反斜杠再转义双引号，否则第一步插入的 \ 会被第二步二次转义
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}
