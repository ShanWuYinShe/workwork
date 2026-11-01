package transfer

import (
	"admin/internal/platform"
	"admin/internal/runtime"
	"crypto/rand"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
)

type Store struct {
	mu     sync.Mutex
	layout runtime.Layout
}

func NewStore(layout runtime.Layout) *Store {
	return &Store{layout: layout}
}

func (s *Store) dir() string          { return s.layout.TransferDir() }
func (s *Store) manifestPath() string { return s.layout.TransferManifestPath() }
func (s *Store) filePath(code, filename string) string {
	return filepath.Join(s.dir(), code+filepath.Ext(filename))
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

func (s *Store) loadLocked() (transferManifest, error) {
	var m transferManifest
	if err := platform.LoadJSONFile(s.manifestPath(), &m); err != nil {
		return transferManifest{}, err
	}
	return m, nil
}

func (s *Store) saveLocked(m transferManifest) error {
	return platform.SaveJSONFile(s.manifestPath(), m)
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
		// "manifest" 保留:该 code + ".json" 扩展名会与 transferManifestPath
		// 同目录同名,上传落盘会覆盖 manifest 本身(概率 62^-8,零成本设防)。
		if !existing[code] && code != reservedTransferCode {
			return code, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique code after 50 attempts")
}

func formatBytes(b int64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	}
	if b >= 1<<20 {
		return fmt.Sprintf("%.0f MB", float64(b)/float64(1<<20))
	}
	return fmt.Sprintf("%d B", b)
}
