package downloads

// Xray 工具下载管理：平台表、资产解析规则与 HTTP handler。
// 机制（manifest/下载/清理/校验/版本比较）在 downloads.go。

import (
	"admin/internal/runtime"
	"net/http"
	"strings"
)

// Xray platform definitions matching GitHub asset naming
var xrayPlatforms = []downloadPlatform{
	{"Linux x64", "linux-64"},
	{"Linux ARM64", "linux-arm64-v8a"},
	{"macOS x64", "macos-64"},
	{"macOS ARM64", "macos-arm64-v8a"},
	{"Windows x64", "windows-64"},
	{"Windows ARM64", "windows-arm64-v8a"},
}

// parseAssetPlatXray 从 "Xray-<plat>.zip" 提取平台标识。
func parseAssetPlatXray(name string) string {
	if !strings.HasPrefix(name, "Xray-") {
		return ""
	}
	rest := strings.TrimPrefix(name, "Xray-")
	rest = strings.TrimSuffix(rest, ".zip")
	for _, p := range xrayPlatforms {
		if rest == p.Plat {
			return p.Plat
		}
	}
	return ""
}

func buildXrayFilename(version, plat, assetName string) string {
	return "Xray-" + version + "-" + plat + ".zip"
}

func newXrayDownloads(layout runtime.Layout) *downloadService {
	return &downloadService{
		repoURL:           "https://api.github.com/repos/XTLS/Xray-core/releases",
		perPage:           20,
		platforms:         xrayPlatforms,
		parseAssetPlat:    parseAssetPlatXray,
		keepChecksumsFile: false,
		dropEmptyReleases: true, // 无资产的 release 跳过（与原行为一致）
		buildFilename:     buildXrayFilename,
		// serve/delete 额外接受历史小写 "xray-" 前缀（旧文件名兼容）
		filenamePrefixes: []string{"Xray-", "xray-"},
		dir:              layout.XrayDownloadsDir(),
		manifestPath:     layout.XrayManifestPath(),
		maxKeep:          5,
	}
}

// GET /api/admin/xray/releases
func (m *Manager) XrayReleases() http.HandlerFunc {
	return releasesHandler(m.xray)
}

// GET /api/admin/xray/platforms/{version}
func (m *Manager) XrayPlatforms() http.HandlerFunc {
	return platformsHandler(m.xray)
}

// GET /api/admin/xray/stored
func (m *Manager) XrayStored() http.HandlerFunc {
	return storedHandler(m.xray)
}

// POST /api/admin/xray/refresh
func (m *Manager) XrayRefresh() http.HandlerFunc {
	return refreshHandler(m.xray, "xray")
}

// POST /api/admin/xray/download
func (m *Manager) XrayDownload() http.HandlerFunc {
	return handleToolDownload(m.xray)
}

// GET /api/admin/xray/downloads/{filename}
func (m *Manager) XrayServeDownload() http.HandlerFunc {
	return serveToolDownload(m.xray, "application/zip")
}

// DELETE /api/admin/xray/downloads
func (m *Manager) XrayDeleteDownload() http.HandlerFunc {
	return deleteToolDownload(m.xray)
}
