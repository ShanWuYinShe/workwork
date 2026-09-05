package main

// Claude Code 工具下载管理：平台表、资产解析规则与 HTTP handler。
// 机制（manifest/下载/清理/校验/版本比较）在 downloads.go，本文件只保留
// claude 特有的参数与接口层。

import (
	"encoding/json"
	"fmt"
	"log"
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

var claudeDownloads = &downloadService{
	repoURL:           "https://api.github.com/repos/anthropics/claude-code/releases",
	perPage:           30,
	platforms:         claudePlatforms,
	parseAssetPlat:    parseAssetPlatClaude,
	keepChecksumsFile: true,  // SHASUMS256.txt 用于完整性校验
	dropEmptyReleases: false, // 无资产的 release 也保留（与原行为一致）
	buildFilename:     buildClaudeFilename,
	filenamePrefixes:  []string{"claude-"},
	dir:               "/app/data/claude-code",
	manifestPath:      "/app/data/claude-code/downloads.json",
	maxKeep:           10,
}

// GET /api/admin/claude-code/releases — admin sees full version list from GitHub
func apiClaudeCodeReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releases, err := claudeDownloads.fetchVersions()
		if err != nil {
			log.Printf("Failed to fetch versions: %v", err)
			writeInternalError(w, err)
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
		writeJSON(w, map[string]any{
			"releases":  out,
			"platforms": claudePlatforms,
			"stored":    claudeDownloads.listStored(),
		})
	}
}

// GET /api/admin/claude-code/platforms/{version} — available platforms from release assets
func apiClaudeCodePlatforms() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if version == "" {
			writeError(w, "Version required", 400)
			return
		}
		release := claudeDownloads.findCachedRelease(version)
		if release == nil {
			writeJSON(w, map[string]any{"version": version, "platforms": []string{}})
			return
		}
		assetPlats := map[string]bool{}
		for _, a := range release.Assets {
			if plat := parseAssetPlatClaude(a.Name); plat != "" {
				assetPlats[plat] = true
			}
		}
		var available []map[string]string
		for _, p := range claudePlatforms {
			if assetPlats[p.Plat] {
				available = append(available, map[string]string{"label": p.Label, "plat": p.Plat})
			}
		}
		writeJSON(w, map[string]any{"version": version, "platforms": available})
	}
}

// GET /api/admin/claude-code/stored — viewer+ can see downloaded files
func apiClaudeCodeStored() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stored := claudeDownloads.listStored()
		if stored == nil {
			stored = []storedDownload{}
		}
		writeJSON(w, map[string]any{"stored": stored})
	}
}

// POST /api/admin/claude-code/refresh — force-refresh version list from GitHub
func apiClaudeCodeRefresh() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claudeDownloads.invalidateCache()
		releases, err := claudeDownloads.fetchVersions()
		if err != nil {
			log.Printf("Failed to refresh: %v", err)
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]any{"status": "ok", "count": len(releases)})
	}
}

// POST /api/admin/claude-code/download — streams NDJSON progress
func apiClaudeCodeDownload() http.HandlerFunc {
	return handleToolDownload(claudeDownloads)
}

// GET /api/admin/claude-code/downloads/{filename}
func apiClaudeCodeServeDownload() http.HandlerFunc {
	return serveToolDownload(claudeDownloads, "application/octet-stream")
}

// DELETE /api/admin/claude-code/downloads
func apiClaudeCodeDeleteDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deleteToolDownload(claudeDownloads)(w, r)
	}
}

// ── 共享 handler（claude / xray 同构，此前逐行复制两份）──

// handleToolDownload 处理 POST download：校验 version/plat、查缓存资产、
// 去重后以 NDJSON 流式回报进度。差异点全部来自 service 描述符。
func handleToolDownload(s *downloadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Version string `json:"version"`
			Plat    string `json:"plat"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.Version == "" || body.Plat == "" {
			writeError(w, "Version and plat required", 400)
			return
		}

		if !s.validPlatform(body.Plat) {
			writeError(w, "Invalid platform: "+body.Plat, 400)
			return
		}

		// Find the asset for this version+plat from cached GitHub releases
		release := s.findCachedRelease(body.Version)
		if release == nil {
			writeError(w, "Version not found in releases", 404)
			return
		}
		// tag 会拼进下载文件名直达文件系统路径:拒绝含路径分隔符等字符的 tag
		// (防御被攻陷的 GitHub 仓库 tag 造成路径穿越)。
		if !safeVersionTag.MatchString(body.Version) {
			writeError(w, "Invalid version tag", 400)
			return
		}
		assetURL := ""
		filename := ""
		for _, a := range release.Assets {
			if s.parseAssetPlat(a.Name) == body.Plat {
				assetURL = a.BrowserDownloadURL
				filename = s.buildFilename(body.Version, body.Plat, a.Name)
				break
			}
		}
		if assetURL == "" {
			writeError(w, "No asset for platform "+body.Plat+" in version "+body.Version, 400)
			return
		}

		for _, sd := range s.listStored() {
			if sd.Version == body.Version && sd.Plat == body.Plat {
				writeJSON(w, map[string]any{"status": "already_exists", "filename": sd.Filename})
				return
			}
		}

		// Stream progress as NDJSON
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, "Streaming not supported", 500)
			return
		}

		sendEvent := func(data map[string]any) bool {
			// 客户端断开后 ResponseWriter 写入返回错误。继续读 CDN 是 CPU/带宽浪费，
			// 检测到 ctx 已取消或写入失败就停止往死管道写。
			if ctxErr := r.Context().Err(); ctxErr != nil {
				return false
			}
			b, _ := json.Marshal(data)
			if _, err := w.Write(b); err != nil {
				return false
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return false
			}
			flusher.Flush()
			return true
		}

		if !sendEvent(map[string]any{"type": "start", "filename": filename}) {
			return
		}

		var lastPct int64 = -1
		clientGone := false
		size, checksum, err := s.downloadArchive(r.Context(), filename, body.Version, body.Plat, assetURL, func(bytes, total int64) {
			if clientGone || total <= 0 {
				return
			}
			pct := bytes * 100 / total
			if pct >= lastPct+5 || bytes >= total {
				lastPct = pct
				if !sendEvent(map[string]any{"type": "progress", "bytes": bytes, "total": total, "percent": pct}) {
					clientGone = true
				}
			}
		})
		if err != nil {
			log.Printf("download failed (%s): %v", s.manifestPath, err)
			sendEvent(map[string]any{"type": "error", "error": "Download failed"})
			return
		}

		s.cleanupOld()
		if !sendEvent(map[string]any{"type": "done", "filename": filename, "size": size, "checksum": checksum}) {
			return
		}
	}
}

// serveToolDownload 处理 GET downloads/{filename}。
func serveToolDownload(s *downloadService, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !validStoredFilename(filename, s.filenamePrefixes...) {
			writeError(w, "Invalid filename", 400)
			return
		}
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeHeaderValue(filename)))
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeFile(w, r, joinDownloadPath(s.dir, filename))
	}
}

// deleteToolDownload 处理 DELETE downloads。
func deleteToolDownload(s *downloadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Filename string `json:"filename"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.Filename == "" {
			writeError(w, "Filename required", 400)
			return
		}
		if !validStoredFilename(body.Filename, s.filenamePrefixes...) {
			writeError(w, "Invalid filename", 400)
			return
		}
		if err := s.deleteStored(body.Filename); err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]any{"status": "ok"})
	}
}
