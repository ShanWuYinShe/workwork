package downloads

// Claude Code 工具下载管理：平台表、资产解析规则与 HTTP handler。
// 机制（manifest/下载/清理/校验/版本比较）在 downloads.go，本文件只保留
// claude 特有的参数与接口层。

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
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
func ApiClaudeCodeReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releases, err := claudeDownloads.fetchVersions()
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
			"platforms": claudePlatforms,
			"stored":    claudeDownloads.listStored(),
		})
	}
}

// GET /api/admin/claude-code/platforms/{version} — available platforms from release assets
func ApiClaudeCodePlatforms() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if version == "" {
			platform.WriteError(w, "版本号必填", 400)
			return
		}
		release := claudeDownloads.findCachedRelease(version)
		if release == nil {
			platform.WriteJSON(w, map[string]any{"version": version, "platforms": []string{}})
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
		platform.WriteJSON(w, map[string]any{"version": version, "platforms": available})
	}
}

// GET /api/admin/claude-code/stored — viewer+ can see downloaded files
func ApiClaudeCodeStored() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stored := claudeDownloads.listStored()
		if stored == nil {
			stored = []storedDownload{}
		}
		platform.WriteJSON(w, map[string]any{"stored": stored})
	}
}

// POST /api/admin/claude-code/refresh — force-refresh version list from GitHub
func ApiClaudeCodeRefresh() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claudeDownloads.invalidateCache()
		releases, err := claudeDownloads.fetchVersions()
		if err != nil {
			log.Printf("Failed to refresh: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] tool refresh tool=claude-code count=%d by=%s", len(releases), actor)
		platform.WriteJSON(w, map[string]any{"status": "ok", "count": len(releases)})
	}
}

// POST /api/admin/claude-code/download — streams NDJSON progress
func ApiClaudeCodeDownload() http.HandlerFunc {
	return handleToolDownload(claudeDownloads)
}

// GET /api/admin/claude-code/downloads/{filename}
func ApiClaudeCodeServeDownload() http.HandlerFunc {
	return serveToolDownload(claudeDownloads, "application/octet-stream")
}

// DELETE /api/admin/claude-code/downloads
func ApiClaudeCodeDeleteDownload() http.HandlerFunc {
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
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "请求体不是合法 JSON", 400)
			return
		}
		if body.Version == "" || body.Plat == "" {
			platform.WriteError(w, "版本号与平台必填", 400)
			return
		}

		if !s.validPlatform(body.Plat) {
			platform.WriteError(w, fmt.Sprintf("平台不合法: %s", body.Plat), 400)
			return
		}

		// Find the asset for this version+plat from cached GitHub releases
		release := s.findCachedRelease(body.Version)
		if release == nil {
			platform.WriteError(w, "该版本不在发布列表中", 404)
			return
		}
		// tag 会拼进下载文件名直达文件系统路径:拒绝含路径分隔符等字符的 tag
		// (防御被攻陷的 GitHub 仓库 tag 造成路径穿越)。
		if !safeVersionTag.MatchString(body.Version) {
			platform.WriteError(w, "版本号格式不合法", 400)
			return
		}
		assetURL := ""
		assetName := ""
		filename := ""
		// 资产摘要:GitHub API 为每个 release 资产返回 "sha256:<hex>",下载完
		// 比对实际内容(此前只有 claude 面靠 SHASUMS256.txt 校验,xray 侧零校验)。
		assetDigest := ""
		for _, a := range release.Assets {
			if s.parseAssetPlat(a.Name) == body.Plat {
				assetURL = a.BrowserDownloadURL
				assetName = a.Name
				assetDigest = a.Digest
				filename = s.buildFilename(body.Version, body.Plat, a.Name)
				break
			}
		}
		if assetURL == "" {
			platform.WriteError(w, fmt.Sprintf("版本 %s 没有 %s 平台的安装包", body.Version, body.Plat), 400)
			return
		}

		// Stream progress as NDJSON
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		// 进度流要实时到达浏览器:nginx /api/admin/ 默认开 proxy_buffering,
		// 不关缓冲会把 NDJSON 攒到响应结束才发,前端进度条全程停摆
		// (SSE 日志流有专门的关缓冲 location,transfer 下载带头,唯独此处漏)。
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			platform.WriteError(w, "当前连接不支持流式响应", 500)
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

		// 已存在分支也走 NDJSON 事件协议:客户端逐行解析且只认 {type:...}。此前
		// 回的是纯 JSON 对象({"status":"already_exists"}),客户端拿不到 type 就抛
		// "Download completed but no result received"——存储列表过期(其他会话/
		// 浏览器已下载)时用户看到的是误导性失败,而不是"已下载过",前端
		// already_exists 分支因此永远不可达。
		for _, sd := range s.listStored() {
			if sd.Version == body.Version && sd.Plat == body.Plat {
				sendEvent(map[string]any{"type": "done", "status": "already_exists", "filename": sd.Filename})
				return
			}
		}

		if !sendEvent(map[string]any{"type": "start", "filename": filename}) {
			return
		}

		var lastPct int64 = -1
		clientGone := false
		size, checksum, err := s.downloadArchive(r.Context(), filename, body.Version, body.Plat, assetURL, assetSHA256(assetDigest), func(bytes, total int64) {
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
			// 摘要不符属安全事件(CDN 调包/传输截断),与普通网络失败分开:留
			// [security] 痕迹,并提示刷新发布列表(资产在缓存后被替换时摘要会过期)。
			if errors.Is(err, errChecksumMismatch) {
				log.Printf("[security] checksum verify failed tool=%s version=%s file=%s: %v",
					filepath.Base(s.dir), body.Version, filename, err)
				sendEvent(map[string]any{"type": "error", "error": "安装包校验失败（与发布清单不一致），请刷新发布列表后重试"})
				return
			}
			log.Printf("download failed (%s): %v", s.manifestPath, err)
			sendEvent(map[string]any{"type": "error", "error": "Download failed"})
			return
		}

		// 官方校验和比对(keepChecksumsFile 的下载面):不符即删产物并报错,
		// 校验不过的分发文件绝不出库。
		if s.keepChecksumsFile {
			if err := s.verifySHASUMS256(release, assetName, checksum); err != nil {
				if rmErr := s.deleteStored(filename); rmErr != nil {
					// 删除失败时条目仍在 manifest,管理员可见可删,不算孤儿。
					log.Printf("ERROR: remove failed download %s after checksum mismatch: %v", filename, rmErr)
				}
				log.Printf("[security] checksum verify failed tool=%s version=%s file=%s: %v",
					filepath.Base(s.dir), body.Version, filename, err)
				sendEvent(map[string]any{"type": "error", "error": "Checksum verification failed"})
				return
			}
		}

		s.cleanupOld()
		if !sendEvent(map[string]any{"type": "done", "filename": filename, "size": size, "checksum": checksum}) {
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] tool download tool=%s version=%s plat=%s file=%s by=%s",
			filepath.Base(s.dir), body.Version, body.Plat, filename, actor)
	}
}

// serveToolDownload 处理 GET downloads/{filename}。
func serveToolDownload(s *downloadService, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !validStoredFilename(filename, s.filenamePrefixes...) {
			platform.WriteError(w, "文件名不合法", 400)
			return
		}
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, platform.SanitizeHeaderValue(filename)))
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeFile(w, r, joinDownloadPath(s.dir, filename))
	}
}

// deleteToolDownload 处理 DELETE downloads。
func deleteToolDownload(s *downloadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// DELETE 不带 body(风格统一):目标经 ?filename= 定位。
		filename := r.URL.Query().Get("filename")
		if filename == "" {
			platform.WriteError(w, "文件名必填", 400)
			return
		}
		if !validStoredFilename(filename, s.filenamePrefixes...) {
			platform.WriteError(w, "文件名不合法", 400)
			return
		}
		if err := s.deleteStored(filename); err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] tool download delete tool=%s file=%q by=%s", filepath.Base(s.dir), filename, actor)
		platform.WriteJSON(w, map[string]any{"status": "ok"})
	}
}
