package downloads

// Xray 工具下载管理：平台表、资产解析规则与 HTTP handler。
// 机制（manifest/下载/清理/校验/版本比较）在 downloads.go。

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"log"
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

var xrayDownloads = &downloadService{
	repoURL:           "https://api.github.com/repos/XTLS/Xray-core/releases",
	perPage:           20,
	platforms:         xrayPlatforms,
	parseAssetPlat:    parseAssetPlatXray,
	keepChecksumsFile: false,
	dropEmptyReleases: true, // 无资产的 release 跳过（与原行为一致）
	buildFilename:     buildXrayFilename,
	// serve/delete 额外接受历史小写 "xray-" 前缀（旧文件名兼容）
	filenamePrefixes: []string{"Xray-", "xray-"},
	dir:              "/app/data/xray",
	manifestPath:     "/app/data/xray/downloads.json",
	maxKeep:          5,
}

// GET /api/admin/xray/releases
func ApiXrayReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releases, err := xrayDownloads.fetchVersions()
		if err != nil {
			log.Printf("Failed to fetch versions: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		type releaseOutput struct {
			Version string `json:"version"`
			Date    string `json:"date"`
		}
		out := make([]releaseOutput, len(releases))
		for i, r := range releases {
			out[i] = releaseOutput{Version: r.Version, Date: r.Date}
		}
		platform.WriteJSON(w, map[string]any{
			"releases":  out,
			"platforms": xrayPlatforms,
			"stored":    xrayDownloads.listStored(),
		})
	}
}

// GET /api/admin/xray/platforms/{version}
func ApiXrayPlatforms() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if version == "" {
			platform.WriteError(w, "Version required", 400)
			return
		}
		release := xrayDownloads.findCachedRelease(version)
		if release == nil {
			platform.WriteJSON(w, map[string]any{"version": version, "platforms": []string{}})
			return
		}
		assetPlats := map[string]bool{}
		for _, a := range release.Assets {
			if plat := parseAssetPlatXray(a.Name); plat != "" {
				assetPlats[plat] = true
			}
		}
		var available []map[string]string
		for _, p := range xrayPlatforms {
			if assetPlats[p.Plat] {
				available = append(available, map[string]string{"label": p.Label, "plat": p.Plat})
			}
		}
		platform.WriteJSON(w, map[string]any{"version": version, "platforms": available})
	}
}

// GET /api/admin/xray/stored
func ApiXrayStored() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stored := xrayDownloads.listStored()
		if stored == nil {
			stored = []storedDownload{}
		}
		platform.WriteJSON(w, map[string]any{"stored": stored})
	}
}

// POST /api/admin/xray/refresh
func ApiXrayRefresh() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		xrayDownloads.invalidateCache()
		releases, err := xrayDownloads.fetchVersions()
		if err != nil {
			log.Printf("Failed to refresh: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] tool refresh tool=xray count=%d by=%s", len(releases), actor)
		platform.WriteJSON(w, map[string]any{"status": "ok", "count": len(releases)})
	}
}

// POST /api/admin/xray/download
func ApiXrayDownload() http.HandlerFunc {
	return handleToolDownload(xrayDownloads)
}

// GET /api/admin/xray/downloads/{filename}
func ApiXrayServeDownload() http.HandlerFunc {
	return serveToolDownload(xrayDownloads, "application/zip")
}

// DELETE /api/admin/xray/downloads
func ApiXrayDeleteDownload() http.HandlerFunc {
	return deleteToolDownload(xrayDownloads)
}
