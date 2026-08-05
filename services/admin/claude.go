package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var githubClient = &http.Client{Timeout: 15 * time.Second}

var downloadClient = &http.Client{Timeout: 10 * time.Minute}

// Platform definitions matching GitHub asset naming
var claudePlatforms = []struct {
	Label string `json:"label"`
	Plat  string `json:"plat"`
}{
	{"Linux x64", "linux-x64"},
	{"Linux x64 (musl)", "linux-x64-musl"},
	{"Linux ARM64", "linux-arm64"},
	{"Linux ARM64 (musl)", "linux-arm64-musl"},
	{"macOS Apple Silicon", "darwin-arm64"},
	{"macOS Intel", "darwin-x64"},
	{"Windows x64", "win32-x64"},
	{"Windows ARM64", "win32-arm64"},
}

type claudeReleaseAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type claudeRelease struct {
	Version string               `json:"version"`
	Date    string               `json:"date"`
	Assets  []claudeReleaseAsset `json:"-"`
}

type claudeStoredDownload struct {
	Version      string `json:"version"`
	Plat         string `json:"plat"`
	PlatformName string `json:"platform_name"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	Checksum     string `json:"checksum"`
	DownloadedAt string `json:"downloaded_at"`
}

var (
	claudeDownloadsDir = "/app/data/claude-code"
	claudeManifestPath = "/app/data/claude-code/downloads.json"
	claudeCache        struct {
		releases  []claudeRelease
		checkedAt time.Time
	}
	claudeCacheMu    sync.Mutex
	claudeDownloadMu sync.Mutex
)

// claude downloads manifest — persisted across restarts
type claudeDownloadsManifest struct {
	Downloads []claudeStoredDownload `json:"downloads"`
}

func loadDownloadsManifest() claudeDownloadsManifest {
	claudeDownloadMu.Lock()
	defer claudeDownloadMu.Unlock()
	return readDownloadsManifestLocked()
}

func readDownloadsManifestLocked() claudeDownloadsManifest {
	var m claudeDownloadsManifest
	loadJSONFile(claudeManifestPath, &m)
	return m
}

func saveDownloadsManifestLocked(m claudeDownloadsManifest) error {
	return saveJSONFile(claudeManifestPath, m)
}

// GET /api/admin/claude-code/releases — admin sees full version list from GitHub
func apiClaudeCodeReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releases, err := fetchClaudeCodeVersions()
		if err != nil {
			log.Printf("Failed to fetch versions: %v", err)
			writeInternalError(w, err)
			return
		}
		stored := listStoredDownloads()
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
			"stored":    stored,
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
		release := findCachedRelease(version)
		if release == nil {
			writeJSON(w, map[string]any{"version": version, "platforms": []string{}})
			return
		}
		assetPlats := map[string]bool{}
		for _, a := range release.Assets {
			plat := parseAssetPlat(a.Name)
			if plat != "" {
				assetPlats[plat] = true
			}
		}
		var available []map[string]string
		for _, p := range claudePlatforms {
			if assetPlats[p.Plat] {
				available = append(available, map[string]string{
					"label": p.Label,
					"plat":  p.Plat,
				})
			}
		}
		writeJSON(w, map[string]any{"version": version, "platforms": available})
	}
}

// GET /api/admin/claude-code/stored — viewer+ can see downloaded files
func apiClaudeCodeStored() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stored := listStoredDownloads()
		if stored == nil {
			stored = []claudeStoredDownload{}
		}
		writeJSON(w, map[string]any{"stored": stored})
	}
}

// POST /api/admin/claude-code/refresh — force-refresh version list from GitHub
func apiClaudeCodeRefresh() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claudeCacheMu.Lock()
		claudeCache.checkedAt = time.Time{}
		claudeCacheMu.Unlock()

		releases, err := fetchClaudeCodeVersions()
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

		valid := false
		for _, p := range claudePlatforms {
			if p.Plat == body.Plat {
				valid = true
				break
			}
		}
		if !valid {
			writeError(w, "Invalid platform: "+body.Plat, 400)
			return
		}

		// Find the asset for this version+plat from cached GitHub releases
		release := findCachedRelease(body.Version)
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
			if parseAssetPlat(a.Name) == body.Plat {
				assetURL = a.BrowserDownloadURL
				ext := ".tar.gz"
				if strings.HasSuffix(a.Name, ".zip") {
					ext = ".zip"
				}
				filename = fmt.Sprintf("claude-%s-%s%s", body.Version, body.Plat, ext)
				break
			}
		}
		if assetURL == "" {
			writeError(w, "No asset for platform "+body.Plat+" in version "+body.Version, 400)
			return
		}

		for _, s := range listStoredDownloads() {
			if s.Version == body.Version && s.Plat == body.Plat {
				writeJSON(w, map[string]any{"status": "already_exists", "filename": s.Filename})
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
		size, checksum, err := downloadClaudeArchive(r.Context(), filename, body.Version, body.Plat, assetURL, func(bytes, total int64) {
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
			log.Printf("claude download failed: %v", err)
			sendEvent(map[string]any{"type": "error", "error": "Download failed"})
			return
		}

		cleanupOldDownloads(10)
		if !sendEvent(map[string]any{"type": "done", "filename": filename, "size": size, "checksum": checksum}) {
			return
		}
	}
}

// GET /api/admin/claude-code/downloads/{filename}
func apiClaudeCodeServeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !strings.HasPrefix(filename, "claude-") ||
			strings.Contains(filename, "..") || strings.Contains(filename, "/") {
			writeError(w, "Invalid filename", 400)
			return
		}
		path := filepath.Join(claudeDownloadsDir, filename)
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeHeaderValue(filename)))
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeFile(w, r, path)
	}
}

// DELETE /api/admin/claude-code/downloads
func apiClaudeCodeDeleteDownload() http.HandlerFunc {
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
		if !strings.HasPrefix(body.Filename, "claude-") ||
			strings.Contains(body.Filename, "..") || strings.Contains(body.Filename, "/") {
			writeError(w, "Invalid filename", 400)
			return
		}
		if err := os.Remove(filepath.Join(claudeDownloadsDir, body.Filename)); err != nil {
			writeInternalError(w, err)
			return
		}
		claudeDownloadMu.Lock()
		m := readDownloadsManifestLocked()
		var kept []claudeStoredDownload
		for _, d := range m.Downloads {
			if d.Filename != body.Filename {
				kept = append(kept, d)
			}
		}
		m.Downloads = kept
		if err := saveDownloadsManifestLocked(m); err != nil {
			claudeDownloadMu.Unlock()
			log.Printf("ERROR: save claude manifest (delete): %v", err)
			writeInternalError(w, fmt.Errorf("persist claude manifest: %w", err))
			return
		}
		claudeDownloadMu.Unlock()
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

func fetchClaudeCodeVersions() ([]claudeRelease, error) {
	claudeCacheMu.Lock()
	if time.Since(claudeCache.checkedAt) < 30*time.Minute && len(claudeCache.releases) > 0 {
		releases := claudeCache.releases
		claudeCacheMu.Unlock()
		return releases, nil
	}
	claudeCacheMu.Unlock()

	req, _ := http.NewRequest("GET", "https://api.github.com/repos/anthropics/claude-code/releases?per_page=30", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "workwork-admin")

	resp, err := githubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 403 限流/5xx 时不盲解 JSON:明确的错误信息,避免 decode 报错误导排查。
		return nil, fmt.Errorf("github api: unexpected status %d", resp.StatusCode)
	}

	var ghReleases []struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			Size               int64  `json:"size"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ghReleases); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}

	var releases []claudeRelease
	for _, r := range ghReleases {
		v := strings.TrimPrefix(r.TagName, "v")
		rel := claudeRelease{Version: v, Date: r.PublishedAt}
		for _, a := range r.Assets {
			if parseAssetPlat(a.Name) != "" || a.Name == "SHASUMS256.txt" {
				rel.Assets = append(rel.Assets, claudeReleaseAsset{
					Name:               a.Name,
					Size:               a.Size,
					BrowserDownloadURL: a.BrowserDownloadURL,
				})
			}
		}
		releases = append(releases, rel)
	}
	sort.Slice(releases, func(i, j int) bool {
		return compareVersions(releases[i].Version, releases[j].Version) > 0
	})

	claudeCacheMu.Lock()
	claudeCache.releases = releases
	claudeCache.checkedAt = time.Now()
	claudeCacheMu.Unlock()

	return releases, nil
}

func findCachedRelease(version string) *claudeRelease {
	claudeCacheMu.Lock()
	defer claudeCacheMu.Unlock()
	for i := range claudeCache.releases {
		if claudeCache.releases[i].Version == version {
			cp := claudeCache.releases[i]
			return &cp
		}
	}
	return nil
}

// parseAssetPlat extracts the platform identifier from a GitHub asset name like "claude-linux-x64.tar.gz"
func parseAssetPlat(name string) string {
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

func listStoredDownloads() []claudeStoredDownload {
	m := loadDownloadsManifest()
	return m.Downloads
}

func parseStoredFilename(name string) (version, plat, platName string) {
	rest := strings.TrimPrefix(name, "claude-")
	for _, suf := range []string{".tar.gz", ".zip"} {
		rest = strings.TrimSuffix(rest, suf)
	}
	plats := make([]struct{ Label, Plat string }, len(claudePlatforms))
	for i, p := range claudePlatforms {
		plats[i] = struct{ Label, Plat string }{p.Label, p.Plat}
	}
	sort.Slice(plats, func(i, j int) bool { return len(plats[i].Plat) > len(plats[j].Plat) })
	for _, p := range plats {
		suffix := "-" + p.Plat
		if strings.HasSuffix(rest, suffix) {
			version = rest[:len(rest)-len(suffix)]
			plat = p.Plat
			platName = p.Label
			return
		}
	}
	version = rest
	plat = "unknown"
	return
}

func downloadClaudeArchive(parentCtx context.Context, filename, version, plat, assetURL string, onProgress func(bytes, total int64)) (int64, string, error) {
	if err := os.MkdirAll(claudeDownloadsDir, 0755); err != nil {
		return 0, "", err
	}

	f, err := os.CreateTemp(claudeDownloadsDir, filename+".download-*")
	if err != nil {
		return 0, "", err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)

	// 客户端断开(来自请求 ctx)或 5 分钟硬超时，任一触发即取消下载请求。
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", assetURL, nil)
	if err != nil {
		f.Close()
		return 0, "", fmt.Errorf("download request: %w", err)
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		f.Close()
		return 0, "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		f.Close()
		return 0, "", fmt.Errorf("HTTP %d downloading asset", resp.StatusCode)
	}

	// Reject the download if the upstream claims a payload larger than
	// maxDownloadSize. Without this check a malicious or compromised CDN
	// could fill the container's disk.
	total := resp.ContentLength
	if total > maxDownloadSize {
		f.Close()
		return 0, "", fmt.Errorf("asset too large: %d bytes (max %d)", total, maxDownloadSize)
	}
	buf := make([]byte, 128*1024)
	hash := sha256.New()
	var written int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxDownloadSize {
				f.Close()
				return 0, "", fmt.Errorf("asset exceeded max size %d", maxDownloadSize)
			}
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return 0, "", fmt.Errorf("write: %w", err)
			}
			hash.Write(buf[:n])
			if onProgress != nil {
				onProgress(written, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			return 0, "", readErr
		}
	}
	if err := f.Close(); err != nil {
		return 0, "", fmt.Errorf("close: %w", err)
	}

	checksum := hex.EncodeToString(hash.Sum(nil))

	finalPath := filepath.Join(claudeDownloadsDir, filename)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return 0, "", err
	}

	platName := ""
	for _, p := range claudePlatforms {
		if p.Plat == plat {
			platName = p.Label
			break
		}
	}

	claudeDownloadMu.Lock()
	m := readDownloadsManifestLocked()
	// 防止并发下载同一 version+plat 导致 manifest 重复条目（文件因 rename 已覆盖一致）。
	dup := false
	for _, d := range m.Downloads {
		if d.Filename == filename {
			dup = true
			break
		}
	}
	if !dup {
		m.Downloads = append(m.Downloads, claudeStoredDownload{
			Version:      version,
			Plat:         plat,
			PlatformName: platName,
			Filename:     filename,
			Size:         written,
			Checksum:     checksum,
			DownloadedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err := saveDownloadsManifestLocked(m); err != nil {
			claudeDownloadMu.Unlock()
			return 0, "", fmt.Errorf("persist claude manifest: %w", err)
		}
	}
	claudeDownloadMu.Unlock()

	return written, checksum, nil
}

func cleanupOldDownloads(maxKeep int) {
	claudeDownloadMu.Lock()
	defer claudeDownloadMu.Unlock()
	m := readDownloadsManifestLocked()
	if len(m.Downloads) <= maxKeep {
		return
	}
	for _, d := range m.Downloads[maxKeep:] {
		os.Remove(filepath.Join(claudeDownloadsDir, d.Filename))
	}
	m.Downloads = m.Downloads[:maxKeep]
	if err := saveDownloadsManifestLocked(m); err != nil {
		log.Printf("ERROR: save claude manifest (cleanup): %v", err)
	}
}

func compareVersions(a, b string) int {
	ap := parseVersionParts(strings.TrimPrefix(a, "v"))
	bp := parseVersionParts(strings.TrimPrefix(b, "v"))
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] != bp[i] {
			return ap[i] - bp[i]
		}
	}
	return len(ap) - len(bp)
}

func parseVersionParts(v string) []int {
	parts := strings.Split(v, ".")
	var nums []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			if idx := strings.IndexAny(p, "-+"); idx > 0 {
				if n2, err2 := strconv.Atoi(p[:idx]); err2 == nil {
					nums = append(nums, n2)
					continue
				}
			}
			nums = append(nums, 0)
			continue
		}
		nums = append(nums, n)
	}
	return nums
}
