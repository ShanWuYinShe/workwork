package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Official Claude Code download base URL (from install.sh)
const claudeCodeBaseURL = "https://downloads.claude.ai/claude-code-releases"

var githubClient = &http.Client{Timeout: 15 * time.Second}

// Platform definitions matching official install.sh naming
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

type claudeRelease struct {
	Version string `json:"version"`
	Date    string `json:"date"`
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

type claudeManifest struct {
	Platforms map[string]struct {
		Checksum string `json:"checksum"`
	} `json:"platforms"`
}

var (
	claudeDownloadsDir = "/app/data/claude-code"
	claudeManifestPath = "/app/data/claude-code/downloads.json"
	claudeCache        struct {
		releases  []claudeRelease
		checkedAt time.Time
	}
	claudeCacheMu sync.Mutex
)

// claude downloads manifest — persisted across restarts
type claudeDownloadsManifest struct {
	Downloads []claudeStoredDownload `json:"downloads"`
}

func loadDownloadsManifest() claudeDownloadsManifest {
	var m claudeDownloadsManifest
	data, err := os.ReadFile(claudeManifestPath)
	if err != nil {
		return m
	}
	json.Unmarshal(data, &m)
	return m
}

func saveDownloadsManifest(m claudeDownloadsManifest) {
	os.MkdirAll(claudeDownloadsDir, 0755)
	data, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(claudeManifestPath, data, 0644)
}

// GET /api/admin/claude-code/releases — admin sees full version list from GitHub
func apiClaudeCodeReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releases, err := fetchClaudeCodeVersions()
		if err != nil {
			writeError(w, "Failed to fetch versions: "+err.Error(), 500)
			return
		}
		stored := listStoredDownloads()
		writeJSON(w, map[string]any{
			"releases":  releases,
			"platforms": claudePlatforms,
			"stored":    stored,
		})
	}
}

// GET /api/admin/claude-code/platforms/{version} — real available platforms for a version
func apiClaudeCodePlatforms() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := r.PathValue("version")
		if version == "" {
			writeError(w, "Version required", 400)
			return
		}
		manifest, err := fetchManifest(version)
		if err != nil {
			writeJSON(w, map[string]any{"version": version, "platforms": []string{}})
			return
		}
		var available []map[string]string
		for _, p := range claudePlatforms {
			if _, ok := manifest.Platforms[p.Plat]; ok {
				available = append(available, map[string]string{
					"label": p.Label,
					"plat":  p.Plat,
				})
			}
		}
		writeJSON(w, map[string]any{"version": version, "platforms": available})
	}
}

// GET /api/admin/claude-code/stored — viewer+ can see downloaded binaries
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
		// Invalidate cache so next call hits GitHub
		claudeCacheMu.Lock()
		claudeCache.checkedAt = time.Time{}
		claudeCacheMu.Unlock()

		releases, err := fetchClaudeCodeVersions()
		if err != nil {
			writeError(w, "Failed to refresh: "+err.Error(), 500)
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Version == "" || body.Plat == "" {
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

		filename := fmt.Sprintf("claude-code-%s-%s", body.Version, body.Plat)
		for _, s := range listStoredDownloads() {
			if s.Filename == filename {
				writeJSON(w, map[string]any{"status": "already_exists", "filename": filename})
				return
			}
		}

		manifest, err := fetchManifest(body.Version)
		if err != nil {
			writeError(w, "Failed to fetch manifest: "+err.Error(), 500)
			return
		}
		platInfo, ok := manifest.Platforms[body.Plat]
		if !ok {
			writeError(w, "Platform "+body.Plat+" not in manifest for version "+body.Version, 400)
			return
		}

		// Stream progress as NDJSON
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		flusher := w.(http.Flusher)

		sendEvent := func(data map[string]any) {
			b, _ := json.Marshal(data)
			w.Write(b)
			w.Write([]byte("\n"))
			flusher.Flush()
		}

		sendEvent(map[string]any{"type": "start", "filename": filename})

		size, err := downloadClaudeBinary(body.Version, body.Plat, filename, platInfo.Checksum, func(bytes, total int64) {
			pct := int64(0)
			if total > 0 {
				pct = bytes * 100 / total
			}
			sendEvent(map[string]any{"type": "progress", "bytes": bytes, "total": total, "percent": pct})
		})
		if err != nil {
			sendEvent(map[string]any{"type": "error", "error": err.Error()})
			return
		}

		cleanupOldDownloads(10)
		sendEvent(map[string]any{"type": "done", "filename": filename, "size": size, "checksum": platInfo.Checksum})
	}
}

// GET /api/admin/claude-code/downloads/{filename}
func apiClaudeCodeServeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !strings.HasPrefix(filename, "claude-code-") {
			writeError(w, "Invalid filename", 400)
			return
		}
		path := filepath.Join(claudeDownloadsDir, filename)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		http.ServeFile(w, r, path)
	}
}

// DELETE /api/admin/claude-code/downloads
func apiClaudeCodeDeleteDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Filename string `json:"filename"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Filename == "" {
			writeError(w, "Filename required", 400)
			return
		}
		if !strings.HasPrefix(body.Filename, "claude-code-") ||
			strings.Contains(body.Filename, "..") || strings.Contains(body.Filename, "/") {
			writeError(w, "Invalid filename", 400)
			return
		}
		if err := os.Remove(filepath.Join(claudeDownloadsDir, body.Filename)); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		// Update manifest
		m := loadDownloadsManifest()
		var kept []claudeStoredDownload
		for _, d := range m.Downloads {
			if d.Filename != body.Filename {
				kept = append(kept, d)
			}
		}
		m.Downloads = kept
		saveDownloadsManifest(m)
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

// fetchClaudeCodeVersions fetches release list from GitHub API (cached 30 min in memory)
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

	var ghReleases []struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ghReleases); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}

	var releases []claudeRelease
	for _, r := range ghReleases {
		v := strings.TrimPrefix(r.TagName, "v")
		releases = append(releases, claudeRelease{Version: v, Date: r.PublishedAt})
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

// fetchManifest fetches checksum metadata from the official CDN (only during download)
func fetchManifest(version string) (*claudeManifest, error) {
	url := fmt.Sprintf("%s/%s/manifest.json", claudeCodeBaseURL, version)
	resp, err := githubClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest not found for %s (HTTP %d)", version, resp.StatusCode)
	}
	var m claudeManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

func listStoredDownloads() []claudeStoredDownload {
	m := loadDownloadsManifest()
	return m.Downloads
}

func parseStoredFilename(name string) (version, plat, platName string) {
	rest := strings.TrimPrefix(name, "claude-code-")
	// Try to match platform suffix (longest first to avoid partial matches)
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

func downloadClaudeBinary(version, plat, filename, expectedChecksum string, onProgress func(bytes, total int64)) (int64, error) {
	os.MkdirAll(claudeDownloadsDir, 0755)

	url := fmt.Sprintf("%s/%s/%s/claude", claudeCodeBaseURL, version, plat)

	resp, err := githubClient.Get(url)
	if err != nil {
		return 0, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d for %s/%s", resp.StatusCode, version, plat)
	}

	total := resp.ContentLength
	path := filepath.Join(claudeDownloadsDir, filename)
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return 0, err
	}

	hasher := sha256.New()
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			f.Write(buf[:n])
			hasher.Write(buf[:n])
			written += int64(n)
			if onProgress != nil {
				onProgress(written, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			os.Remove(tmpPath)
			return 0, readErr
		}
	}
	f.Close()

	// Verify SHA256 checksum
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != expectedChecksum {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum[:16], actual[:16])
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}

	// Persist metadata
	m := loadDownloadsManifest()
	platName := ""
	for _, p := range claudePlatforms {
		if p.Plat == plat {
			platName = p.Label
			break
		}
	}
	m.Downloads = append(m.Downloads, claudeStoredDownload{
		Version:      version,
		Plat:         plat,
		PlatformName: platName,
		Filename:     filename,
		Size:         written,
		Checksum:     actual,
		DownloadedAt: time.Now().UTC().Format(time.RFC3339),
	})
	saveDownloadsManifest(m)

	return written, nil
}

func cleanupOldDownloads(maxKeep int) {
	m := loadDownloadsManifest()
	if len(m.Downloads) <= maxKeep {
		return
	}
	for _, d := range m.Downloads[maxKeep:] {
		os.Remove(filepath.Join(claudeDownloadsDir, d.Filename))
	}
	m.Downloads = m.Downloads[:maxKeep]
	saveDownloadsManifest(m)
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
				}
			}
			nums = append(nums, 0)
			continue
		}
		nums = append(nums, n)
	}
	return nums
}
