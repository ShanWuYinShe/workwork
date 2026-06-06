package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
)

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
	data, err := os.ReadFile(transferManifestPath)
	if err != nil {
		return m
	}
	json.Unmarshal(data, &m)
	return m
}

func saveTransferManifestLocked(m transferManifest) {
	os.MkdirAll(transferDir, 0755)
	data, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(transferManifestPath, data, 0600)
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
		saveTransferManifestLocked(m)
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

		// 限制 multipart form 大小为 2GB
		if err := r.ParseMultipartForm(transferMaxFile); err != nil {
			writeError(w, "文件过大（最大 2GB）或请求格式错误", 400)
			return
		}

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
			if n, err := parseInt(v); err == nil && n >= 0 {
				maxDL = n
			}
		}

		transferMu.Lock()
		defer transferMu.Unlock()

		m := loadTransferManifestLocked()

		// 检查容量
		fileSize := header.Size
		if fileSize <= 0 {
			// 尝试读取文件获取大小
			var buf [0]byte
			n, _ := file.Read(buf[:])
			_ = n
			// 使用 Content-Length 作为近似值
			fileSize = r.ContentLength
		}
		if m.TotalSize+fileSize > transferMaxTotal {
			writeError(w, fmt.Sprintf("存储空间不足（已用 %s，上限 20GB）", formatBytes(m.TotalSize)), 400)
			return
		}

		// 生成分享码
		code, err := generateUniqueTransferCode(m)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		// 保存文件到磁盘
		os.MkdirAll(transferDir, 0755)
		dstPath := transferFilePath(code, name)
		tmpPath := dstPath + ".tmp"
		dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		written, err := io.Copy(dst, io.LimitReader(file, transferMaxFile))
		dst.Close()
		if err != nil {
			os.Remove(tmpPath)
			writeInternalError(w, err)
			return
		}
		if err := os.Rename(tmpPath, dstPath); err != nil {
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
		saveTransferManifestLocked(m)

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

		// 先清理过期文件
		cleanupExpiredTransferFiles()

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
			"files":     files,
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
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
		saveTransferManifestLocked(m)
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

		// 增加下载计数
		target.Downloads++
		saveTransferManifestLocked(m)
		transferMu.Unlock()

		// 提供文件下载
		filePath := transferFilePath(code, target.Filename)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", sanitizeHeaderValue(target.Filename)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeFile(w, r, filePath)
	}
}

// parseInt 安全解析正整数
func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid digit: %c", c)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
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
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}
