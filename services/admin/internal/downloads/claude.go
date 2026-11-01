package downloads

// Claude Code 工具下载管理：平台表、资产解析规则与 HTTP handler。
// 机制（manifest/下载/清理/校验/版本比较）在 downloads.go，本文件只保留
// claude 特有的参数与接口层。

import (
	"admin/internal/runtime"
	"fmt"
	"net/http"
	"strings"
)

// Platform definitions matching GitHub asset naming
var claudePlatforms = []downloadPlatform{
	{"Linux x64", "linux-x64"},
	{"Linux x64 (musl)", "linux-x64-musl"},
	{"Linux ARM64", "linux-arm64"},
	{"Linux ARM64 (musl)", "linux-arm64-musl"},
	{"macOS Apple Silicon", "darwin-arm64"},
	{"macOS Intel", "darwin-x64"},
	{"Windows x64", "win32-x64"},
	{"Windows ARM64", "win32-arm64"},
}

// parseAssetPlatClaude 从 "claude-<plat>.tar.gz|.zip" 提取平台标识。
func parseAssetPlatClaude(name string) string {
	rest := strings.TrimPrefix(name, "claude-")
	for _, suf := range []string{".tar.gz", ".zip"} {
		rest = strings.TrimSuffix(rest, suf)
	}
	for _, p := range claudePlatforms {
		if rest == p.Plat {
			return p.Plat
		}
	}
	return ""
}

func buildClaudeFilename(version, plat, assetName string) string {
	ext := ".tar.gz"
	if strings.HasSuffix(assetName, ".zip") {
		ext = ".zip"
	}
	return fmt.Sprintf("claude-%s-%s%s", version, plat, ext)
}

func newClaudeDownloads(layout runtime.Layout) *downloadService {
	return &downloadService{
		repoURL:           "https://api.github.com/repos/anthropics/claude-code/releases",
		perPage:           30,
		platforms:         claudePlatforms,
		parseAssetPlat:    parseAssetPlatClaude,
		keepChecksumsFile: true,  // SHASUMS256.txt 用于完整性校验
		dropEmptyReleases: false, // 无资产的 release 也保留（与原行为一致）
		buildFilename:     buildClaudeFilename,
		filenamePrefixes:  []string{"claude-"},
		dir:               layout.ClaudeDownloadsDir(),
		manifestPath:      layout.ClaudeManifestPath(),
		maxKeep:           10,
	}
}

// GET /api/admin/claude-code/releases — admin sees full version list from GitHub
func (m *Manager) ClaudeCodeReleases() http.HandlerFunc {
	return releasesHandler(m.claude)
}

// GET /api/admin/claude-code/platforms/{version} — available platforms from release assets
func (m *Manager) ClaudeCodePlatforms() http.HandlerFunc {
	return platformsHandler(m.claude)
}

// GET /api/admin/claude-code/stored — viewer+ can see downloaded files
func (m *Manager) ClaudeCodeStored() http.HandlerFunc {
	return storedHandler(m.claude)
}

// POST /api/admin/claude-code/refresh — force-refresh version list from GitHub
func (m *Manager) ClaudeCodeRefresh() http.HandlerFunc {
	return refreshHandler(m.claude, "claude-code")
}

// POST /api/admin/claude-code/download — streams NDJSON progress
func (m *Manager) ClaudeCodeDownload() http.HandlerFunc {
	return handleToolDownload(m.claude)
}

// GET /api/admin/claude-code/downloads/{filename}
func (m *Manager) ClaudeCodeServeDownload() http.HandlerFunc {
	return serveToolDownload(m.claude, "application/octet-stream")
}

// DELETE /api/admin/claude-code/downloads
func (m *Manager) ClaudeCodeDeleteDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deleteToolDownload(m.claude)(w, r)
	}
}
