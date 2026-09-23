package transfer

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"context"
	"crypto/rand"
	"errors"
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

// transferDir/transferManifestPath 是 var 供测试替换(与 musicLibraryDir
// /xrayClientsPath 同一模式);生产路径不变。
var (
	transferDir          = "/app/data/transfer"
	transferManifestPath = "/app/data/transfer/manifest.json"
)

const (
	transferMaxTotal   = 20 << 30 // 20GB
	transferMaxFile    = 2 << 30  // 2GB per file
	transferCodeLen    = 8
	transferDefaultExp = 7 * 24 * time.Hour
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

// validTransferFilename 校验上传文件名:拒绝路径分隔符、控制字符与 Unicode
// 双向排版控制字符(U+202A–202E / U+2066–2069,可用于视觉反转扩展名伪装
// 如 "exe.jar‮exe"),其余(含中文等 Unicode)放行。落盘名是"分享码+扩展名"
// (transferFilePath),原文件名只进 manifest 与下载响应头;响应头经
// mime.FormatMediaType 做 RFC 5987 编码,非 ASCII 与引号等特殊字符均被安全转义。
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
		// bidi 排版控制符:RFC 5893/UTS#39 约束的视觉欺骗字符,文件管理器
		// 会按其反转后续文本的显示方向,可用于伪装扩展名传播恶意文件。
		if (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
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
			// Remove 失败(非 NotExist)时保留条目:文件仍在盘上,条目删除后
			// 列表看不到、删除接口够不到、配额不再计数——成为无任何回收路径
			// 的孤儿。保留条目让下一轮清理重试,并留 ERROR 痕迹。
			if err := os.Remove(transferFilePath(f.Code, f.Filename)); err != nil && !os.IsNotExist(err) {
				log.Printf("ERROR: transfer cleanup remove %s: %v (entry kept for retry)", f.Code, err)
				kept = append(kept, f)
				continue
			}
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

		// 并发闸门(32MB multipart 内存缓冲,全局限 2 位,防并发上传 OOM)。
		release, ok := platform.AcquireUploadSlot(r.Context())
		if !ok {
			platform.WriteError(w, "并发上传过多，请稍后重试", http.StatusTooManyRequests)
			return
		}
		defer release()

		// 请求体硬上限（单文件 2GB + multipart 开销）。ParseMultipartForm 会把
		// 全部 file part 先无上限 spool 到磁盘才返回，header.Size 预检与配额
		// 检查都发生在其后——没有这层，超大请求体先把磁盘写满。nginx 侧的
		// client_max_body_size 只保护经 nginx 的路径，直连 admin 的内部调用
		// 不经过它。
		r.Body = http.MaxBytesReader(w, r.Body, transferMaxFile+64<<20)

		// 限制 multipart 内存缓冲为 32MB；更大的部分自动落盘为临时文件，
		// 避免单次上传占用 2GB 堆内存导致 OOM。单文件上限在下方强制执行。
		if err := r.ParseMultipartForm(transferUploadMemory); err != nil {
			// 请求体超出 MaxBytesReader 上限是容量问题,语义上应为 413;
			// 其余(格式错误)保持 400。http.MaxBytesError 自 Go 1.19 起可
			// 从错误链中识别。
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				platform.WriteError(w, fmt.Sprintf("文件过大（最大 %dGB）", transferMaxFile>>30), http.StatusRequestEntityTooLarge)
				return
			}
			platform.WriteError(w, "请求格式错误", 400)
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
			platform.WriteError(w, fmt.Sprintf("文件超过 %dGB 上限", transferMaxFile>>30), http.StatusRequestEntityTooLarge)
			return
		}

		// 解析有效期:非法值显式拒绝,不再静默回退默认——静默回退会执行与
		// 上传者意图不符的生命周期(以为传 300 天实际 7 天就消失)。
		expiresIn := r.FormValue("expires_in")
		duration := transferDefaultExp
		if expiresIn != "" {
			d, err := time.ParseDuration(expiresIn)
			if err != nil || d <= 0 || d > 365*24*time.Hour {
				platform.WriteError(w, "有效期不合法（须为正的时长，最长 365 天，如 720h）", 400)
				return
			}
			duration = d
		}

		// 解析最大下载次数:同样显式拒绝非法值。
		maxDL := 0
		if v := r.FormValue("max_downloads"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				platform.WriteError(w, "下载上限不合法（须为非负整数，0 表示不限）", 400)
				return
			}
			maxDL = n
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
		if countUploaderEntries(snapshot, username) >= maxTransferFilesPerUser {
			transferMu.Unlock()
			platform.WriteError(w, fmt.Sprintf("文件数量已达上限（每人最多 %d 个）", maxTransferFilesPerUser), 400)
			return
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
		// rename 前 fsync:断电不会留下"manifest 记了 2GB、文件内容为空"的
		// 坏文件(下载端无校验和可发现)。
		if serr := dst.Sync(); serr != nil && err == nil {
			err = serr
		}
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
				platform.WriteError(w, fmt.Sprintf("文件超过 %dGB 上限", transferMaxFile>>30), http.StatusRequestEntityTooLarge)
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
		if countUploaderEntries(m, username) >= maxTransferFilesPerUser {
			transferMu.Unlock()
			os.Remove(tmpPath)
			platform.WriteError(w, fmt.Sprintf("文件数量已达上限（每人最多 %d 个）", maxTransferFilesPerUser), 400)
			return
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
			// 磁盘文件已就位但 manifest 未持久化 → 孤儿文件:不在 manifest 里,
			// 列表看不到、删除接口删不到、且不占配额计数——没有任何回收路径,
			// 配额检查会持续放行,磁盘满时滚雪球。尽力回滚删除;删除也失败
			// (如目录只读)时带路径记 ERROR,交运维手工清理。
			if rmErr := os.Remove(dstPath); rmErr != nil {
				log.Printf("ERROR: transfer orphan cleanup failed, manual removal needed: %s: %v", dstPath, rmErr)
			}
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
		// 分页(可选):limit/offset 显式传入时校验并截取,默认全量。
		page, ok := platform.ParseListPage(w, r)
		if !ok {
			return
		}
		files = platform.Apply(files, page)

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

		// DELETE 不带 body(风格统一,claude/xray downloads 同为 query):目标经 ?code= 定位。
		code := r.URL.Query().Get("code")
		if code == "" {
			platform.WriteError(w, "分享码必填", 400)
			return
		}

		transferMu.Lock()
		defer transferMu.Unlock()

		m := loadTransferManifestLocked()
		found := false
		var kept []transferFile
		var removedNames []string
		for _, f := range m.Files {
			if f.Code == code {
				// 权限检查：admin 可删任意文件，普通用户只能删自己的
				if userInfo != nil && userInfo.Role != "admin" && f.Uploader != username {
					platform.WriteError(w, "无权操作：不是你上传的文件", 403)
					return
				}
				// Remove 失败(非 NotExist)时保留条目并向调用方如实报错:
				// 条目删除后文件成为孤儿(不可见、不可再删、不占配额),保留
				// 条目让用户可以重试删除。
				if err := os.Remove(transferFilePath(f.Code, f.Filename)); err != nil && !os.IsNotExist(err) {
					log.Printf("ERROR: transfer delete remove %s: %v (entry kept)", f.Code, err)
					platform.WriteError(w, "文件删除失败，请稍后重试", 500)
					return
				}
				removedNames = append(removedNames, f.Filename)
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
		// 审计带上命中条目的文件名(服务端可控值,无注入面):事后审计不必
		// 反查 manifest——那时条目已删。
		log.Printf("[security] transfer delete code=%s files=%q by=%s", code, removedNames, username)
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

		// 下载计数移到 ServeContent 之后按"实际交付"判定(见
		// deliveredFullDownload):此前在响应前预计数,HEAD(无响应体)或越界
		// range(416)也各耗一次 MaxDownloads——知道分享码即可打空枪把限次
		// 链接提前打死(已实测复现)。specCoversLast 只表达请求意图,是否
		// 兑现由 countingResponseWriter 的 status/written 确认。
		specCoversLast := rangeCountsAsDownload(r.Header.Get("Range"), target.Size)
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
		cw := &countingResponseWriter{ResponseWriter: w}
		http.ServeContent(cw, r, target.Filename, info.ModTime(), f)

		// 按实际交付结果计数(见 specCoversLast 与 deliveredFullDownload 注释)。
		// 计数持久化失败:不阻断下载本身(文件已读出),仅记录。
		if deliveredFullDownload(r, cw, specCoversLast, info.Size()) {
			transferMu.Lock()
			m2 := loadTransferManifestLocked()
			for i := range m2.Files {
				if m2.Files[i].Code == code {
					m2.Files[i].Downloads++
					if err := saveTransferManifestLocked(m2); err != nil {
						log.Printf("ERROR: save transfer manifest (download count): %v", err)
					}
					break
				}
			}
			transferMu.Unlock()
		}
	}
}

// countingResponseWriter 记录 ServeContent 实际写出的状态码与字节数,供下载
// 计数判断"是否真的交付了内容"。
type countingResponseWriter struct {
	http.ResponseWriter
	status   int
	written  int64
	writeErr bool
}

func (c *countingResponseWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *countingResponseWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	n, err := c.ResponseWriter.Write(p)
	c.written += int64(n)
	if err != nil {
		c.writeErr = true
	}
	return n, err
}

// Flush 透传: ServeContent 在无缓冲场景可能使用 Flusher。
func (c *countingResponseWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// deliveredFullDownload 判定一次请求是否真正消耗了下载次数:排除 HEAD(永无
// 响应体)、416 等错误响应(没有内容发出)、客户端中途断开(writeErr)与不
// 覆盖末字节的中间分段(specCoversLast=false)。200 须发满全文件;206 须请求
// 规格覆盖末字节(下载管理器的收尾分段恰好计 1,完整下载仍恰好计 1)。
func deliveredFullDownload(r *http.Request, cw *countingResponseWriter, specCoversLast bool, size int64) bool {
	if r.Method == http.MethodHead {
		return false
	}
	if cw.writeErr || (cw.status != http.StatusOK && cw.status != http.StatusPartialContent) {
		return false
	}
	if !specCoversLast {
		return false
	}
	if cw.status == http.StatusOK {
		return cw.written >= size
	}
	return cw.written > 0
}

// rangeCountsAsDownload 判断一次（可能带 Range 的）请求"按意图"是否取到
// 文件末字节——它是下载计数的必要条件之一,充分条件由
// deliveredFullDownload 按实际响应(200/206、written、writeErr)确认。
//
// 任何完整下载——单连接无 Range、或多连接下载管理器的某个分段——都必然取到
// 文件的最后一个字节。因此只有"覆盖文件末字节"的规格才计：完整下载恰好计 1
// （取末字节的那个分段），中间续传分段（bytes=1048576-2097151）不计。后缀范围
// bytes=-N（N≥文件大小即取到全文末字节）与 bytes=1-（跳过首字节的开放范围，
// 覆盖末字节）必须计数。解析不了的规格保守按"覆盖"处理——实际是否发出内容由
// ServeContent 的响应状态决定(非法/越界范围得到 416,不会计数)。
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

// DeleteUploadsByUploader 删除某用户的全部上传(文件 + manifest 条目),供
// 删除用户时的级联清理。被删用户无法再自助管理其文件(transfer 列表按
// Uploader 过滤),其配额在删除前永久占用,只能等过期清理——但那期间全局
// 20GB 池被无效占用。返回删除的文件数;无上传时返回 0。
func DeleteUploadsByUploader(uploader string) (int, error) {
	transferMu.Lock()
	defer transferMu.Unlock()

	m := loadTransferManifestLocked()
	removed := 0
	var kept []transferFile
	for _, f := range m.Files {
		if f.Uploader == uploader {
			if err := os.Remove(transferFilePath(f.Code, f.Filename)); err != nil && !os.IsNotExist(err) {
				// 单文件删除失败不中断:manifest 先行收敛,残留文件由过期
				// 清理回收(它按 manifest 条目删,条目已移除——失败文件成为
				// 孤儿,但磁盘满类故障下这是可接受的降级)。
				log.Printf("ERROR: transfer cascade delete %s: %v", f.Code, err)
			}
			m.TotalSize -= f.Size
			removed++
			continue
		}
		kept = append(kept, f)
	}
	if removed == 0 {
		return 0, nil
	}
	m.Files = kept
	if m.TotalSize < 0 {
		m.TotalSize = 0
	}
	if err := saveTransferManifestLocked(m); err != nil {
		return removed, err
	}
	log.Printf("[security] transfer cascade delete uploader=%q removed=%d", uploader, removed)
	return removed, nil
}
