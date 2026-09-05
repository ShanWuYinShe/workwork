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
	"strings"
	"sync"
	"time"
)

// Xray platform definitions matching GitHub asset naming
var xrayPlatforms = []struct {
	Label string `json:"label"`
	Plat  string `json:"plat"`
}{
	{"Linux x64", "linux-64"},
	{"Linux ARM64", "linux-arm64-v8a"},
	{"macOS x64", "macos-64"},
	{"macOS ARM64", "macos-arm64-v8a"},
	{"Windows x64", "windows-64"},
	{"Windows ARM64", "windows-arm64-v8a"},
}

type xrayReleaseAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type xrayRelease struct {
	Version string             `json:"version"`
	Date    string             `json:"date"`
	Assets  []xrayReleaseAsset `json:"-"`
}

type xrayStoredDownload struct {
	Version      string `json:"version"`
	Plat         string `json:"plat"`
	PlatformName string `json:"platform_name"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	Checksum     string `json:"checksum"`
	DownloadedAt string `json:"downloaded_at"`
}

var (
	xrayDownloadsDir = "/app/data/xray"
	xrayManifestPath = "/app/data/xray/downloads.json"
	xrayCache        struct {
		releases  []xrayRelease
		checkedAt time.Time
	}
	xrayCacheMu    sync.Mutex
	xrayDownloadMu sync.Mutex
)

type xrayDownloadsManifest struct {
	Downloads []xrayStoredDownload `json:"downloads"`
}

func loadXrayManifest() xrayDownloadsManifest {
	xrayDownloadMu.Lock()
	defer xrayDownloadMu.Unlock()
	return readXrayManifestLocked()
}

func readXrayManifestLocked() xrayDownloadsManifest {
	var m xrayDownloadsManifest
	loadJSONFile(xrayManifestPath, &m)
	return m
}

func saveXrayManifestLocked(m xrayDownloadsManifest) error {
	return saveJSONFile(xrayManifestPath, m)
}

// GET /api/admin/xray/releases
func apiXrayReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releases, err := fetchXrayVersions()
		if err != nil {
			log.Printf("Failed to fetch versions: %v", err)
			writeInternalError(w, err)
			return
		}
		stored := listXrayStored()
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
			"platforms": xrayPlatforms,
			"stored":    stored,
		})
	}
}

// GET /api/admin/xray/platforms/{version}
func apiXrayPlatforms() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if version == "" {
			writeError(w, "Version required", 400)
			return
		}
		release := findXrayCachedRelease(version)
		if release == nil {
			writeJSON(w, map[string]any{"version": version, "platforms": []string{}})
			return
		}
		assetPlats := map[string]bool{}
		for _, a := range release.Assets {
			plat := parseXrayAssetPlat(a.Name)
			if plat != "" {
				assetPlats[plat] = true
			}
		}
		var available []map[string]string
		for _, p := range xrayPlatforms {
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

// GET /api/admin/xray/stored
func apiXrayStored() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stored := listXrayStored()
		if stored == nil {
			stored = []xrayStoredDownload{}
		}
		writeJSON(w, map[string]any{"stored": stored})
	}
}

// POST /api/admin/xray/refresh
func apiXrayRefresh() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		xrayCacheMu.Lock()
		xrayCache.checkedAt = time.Time{}
		xrayCacheMu.Unlock()

		releases, err := fetchXrayVersions()
		if err != nil {
			log.Printf("Failed to refresh: %v", err)
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]any{"status": "ok", "count": len(releases)})
	}
}

// POST /api/admin/xray/download
func apiXrayDownload() http.HandlerFunc {
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
		for _, p := range xrayPlatforms {
			if p.Plat == body.Plat {
				valid = true
				break
			}
		}
		if !valid {
			writeError(w, "Invalid platform: "+body.Plat, 400)
			return
		}

		release := findXrayCachedRelease(body.Version)
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
			if parseXrayAssetPlat(a.Name) == body.Plat {
				assetURL = a.BrowserDownloadURL
				filename = fmt.Sprintf("Xray-%s-%s.zip", body.Version, body.Plat)
				break
			}
		}
		if assetURL == "" {
			writeError(w, "No asset for platform "+body.Plat+" in version "+body.Version, 400)
			return
		}

		for _, s := range listXrayStored() {
			if s.Version == body.Version && s.Plat == body.Plat {
				writeJSON(w, map[string]any{"status": "already_exists", "filename": s.Filename})
				return
			}
		}

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
		size, checksum, err := downloadXrayArchive(r.Context(), filename, body.Version, body.Plat, assetURL, func(bytes, total int64) {
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
			log.Printf("xray download failed: %v", err)
			sendEvent(map[string]any{"type": "error", "error": "Download failed"})
			return
		}

		cleanupOldXrayDownloads(5)
		if !sendEvent(map[string]any{"type": "done", "filename": filename, "size": size, "checksum": checksum}) {
			return
		}
	}
}

// GET /api/admin/xray/downloads/{filename}
func apiXrayServeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if (!strings.HasPrefix(filename, "Xray-") && !strings.HasPrefix(filename, "xray-")) ||
			strings.Contains(filename, "..") || strings.Contains(filename, "/") ||
			strings.Contains(filename, ".download-") {
			writeError(w, "Invalid filename", 400)
			return
		}
		path := filepath.Join(xrayDownloadsDir, filename)
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeHeaderValue(filename)))
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeFile(w, r, path)
	}
}

// DELETE /api/admin/xray/downloads
func apiXrayDeleteDownload() http.HandlerFunc {
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
		if (!strings.HasPrefix(body.Filename, "Xray-") && !strings.HasPrefix(body.Filename, "xray-")) ||
			strings.Contains(body.Filename, "..") || strings.Contains(body.Filename, "/") ||
			strings.Contains(body.Filename, ".download-") {
			writeError(w, "Invalid filename", 400)
			return
		}
		if err := os.Remove(filepath.Join(xrayDownloadsDir, body.Filename)); err != nil {
			writeInternalError(w, err)
			return
		}
		xrayDownloadMu.Lock()
		m := readXrayManifestLocked()
		var kept []xrayStoredDownload
		for _, d := range m.Downloads {
			if d.Filename != body.Filename {
				kept = append(kept, d)
			}
		}
		m.Downloads = kept
		if err := saveXrayManifestLocked(m); err != nil {
			xrayDownloadMu.Unlock()
			log.Printf("ERROR: save xray manifest (delete): %v", err)
			writeInternalError(w, fmt.Errorf("persist xray manifest: %w", err))
			return
		}
		xrayDownloadMu.Unlock()
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

// xrayFetchMu 合并缓存过期时的并发回源（与 claudeFetchMu 同款 singleflight）：
// 避免并发请求全部直击 GitHub API 打爆匿名限额。
var xrayFetchMu sync.Mutex

func fetchXrayVersions() ([]xrayRelease, error) {
	xrayCacheMu.Lock()
	if time.Since(xrayCache.checkedAt) < 30*time.Minute && len(xrayCache.releases) > 0 {
		releases := xrayCache.releases
		xrayCacheMu.Unlock()
		return releases, nil
	}
	xrayCacheMu.Unlock()

	xrayFetchMu.Lock()
	defer xrayFetchMu.Unlock()
	xrayCacheMu.Lock()
	if time.Since(xrayCache.checkedAt) < 30*time.Minute && len(xrayCache.releases) > 0 {
		releases := xrayCache.releases
		xrayCacheMu.Unlock()
		return releases, nil
	}
	xrayCacheMu.Unlock()

	req, _ := http.NewRequest("GET", "https://api.github.com/repos/XTLS/Xray-core/releases?per_page=20", nil)
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

	var releases []xrayRelease
	for _, r := range ghReleases {
		v := strings.TrimPrefix(r.TagName, "v")
		rel := xrayRelease{Version: v, Date: r.PublishedAt}
		for _, a := range r.Assets {
			if parseXrayAssetPlat(a.Name) != "" {
				rel.Assets = append(rel.Assets, xrayReleaseAsset{
					Name:               a.Name,
					Size:               a.Size,
					BrowserDownloadURL: a.BrowserDownloadURL,
				})
			}
		}
		if len(rel.Assets) > 0 {
			releases = append(releases, rel)
		}
	}
	sort.Slice(releases, func(i, j int) bool {
		return compareVersions(releases[i].Version, releases[j].Version) > 0
	})

	xrayCacheMu.Lock()
	xrayCache.releases = releases
	xrayCache.checkedAt = time.Now()
	xrayCacheMu.Unlock()

	return releases, nil
}

func findXrayCachedRelease(version string) *xrayRelease {
	xrayCacheMu.Lock()
	defer xrayCacheMu.Unlock()
	for i := range xrayCache.releases {
		if xrayCache.releases[i].Version == version {
			cp := xrayCache.releases[i]
			return &cp
		}
	}
	return nil
}

// parseXrayAssetPlat extracts platform from Xray asset names like "Xray-linux-64.zip"
func parseXrayAssetPlat(name string) string {
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

func listXrayStored() []xrayStoredDownload {
	m := loadXrayManifest()
	return m.Downloads
}

func parseXrayStoredFilename(name string) (version, plat, platName string) {
	rest := strings.TrimPrefix(name, "Xray-")
	rest = strings.TrimSuffix(rest, ".zip")
	plats := make([]struct{ Label, Plat string }, len(xrayPlatforms))
	for i, p := range xrayPlatforms {
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

func downloadXrayArchive(parentCtx context.Context, filename, version, plat, assetURL string, onProgress func(bytes, total int64)) (int64, string, error) {
	if err := os.MkdirAll(xrayDownloadsDir, 0755); err != nil {
		return 0, "", err
	}

	f, err := os.CreateTemp(xrayDownloadsDir, filename+".download-*")
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

	finalPath := filepath.Join(xrayDownloadsDir, filename)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return 0, "", err
	}

	platName := ""
	for _, p := range xrayPlatforms {
		if p.Plat == plat {
			platName = p.Label
			break
		}
	}

	xrayDownloadMu.Lock()
	m := readXrayManifestLocked()
	// 防止并发下载同一 version+plat 导致 manifest 重复条目（文件因 rename 已覆盖一致）。
	dup := false
	for _, d := range m.Downloads {
		if d.Filename == filename {
			dup = true
			break
		}
	}
	if !dup {
		m.Downloads = append(m.Downloads, xrayStoredDownload{
			Version:      version,
			Plat:         plat,
			PlatformName: platName,
			Filename:     filename,
			Size:         written,
			Checksum:     checksum,
			DownloadedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err := saveXrayManifestLocked(m); err != nil {
			xrayDownloadMu.Unlock()
			return 0, "", fmt.Errorf("persist xray manifest: %w", err)
		}
	}
	xrayDownloadMu.Unlock()

	return written, checksum, nil
}

func cleanupOldXrayDownloads(maxKeep int) {
	xrayDownloadMu.Lock()
	defer xrayDownloadMu.Unlock()
	m := readXrayManifestLocked()
	if len(m.Downloads) <= maxKeep {
		return
	}
	// 保留最近 maxKeep 条（列表按下载时间追加，末尾最新），淘汰最早的多余条目。
	// 此前反向清理会把刚下载完的文件立即删掉，导致“下载成功但文件不存在”。
	drop := len(m.Downloads) - maxKeep
	kept := keepNewestXrayDownloads(m.Downloads, maxKeep)
	for _, d := range m.Downloads[:drop] {
		os.Remove(filepath.Join(xrayDownloadsDir, d.Filename))
	}
	m.Downloads = kept
	if err := saveXrayManifestLocked(m); err != nil {
		log.Printf("ERROR: save xray manifest (cleanup): %v", err)
	}
}

// keepNewestXrayDownloads 返回最近 maxKeep 条下载记录（含拷贝，避免与入参共享底层数组）。
func keepNewestXrayDownloads(entries []xrayStoredDownload, maxKeep int) []xrayStoredDownload {
	if len(entries) <= maxKeep {
		return append([]xrayStoredDownload{}, entries...)
	}
	drop := len(entries) - maxKeep
	return append([]xrayStoredDownload{}, entries[drop:]...)
}
