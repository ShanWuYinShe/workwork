package main

import (
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

// Official Claude Code binary distribution (Google Cloud Storage)
const claudeCodeBaseURL = "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"

var claudePlatforms = []struct {
	Label  string `json:"label"`
	Plat   string `json:"plat"`
	Ext    string `json:"ext"`
}{
	{"Linux x64", "linux-x64", ""},
	{"Linux ARM64", "linux-arm64", ""},
	{"macOS Apple Silicon", "darwin-arm64", ""},
	{"macOS Intel", "darwin-x64", ""},
	{"Windows x64", "win32-x64", ".exe"},
}

type claudeRelease struct {
	Version string `json:"version"`
	Date    string `json:"date"`
}

type claudePlatformInfo struct {
	Label string `json:"label"`
	Plat  string `json:"plat"`
	Ext   string `json:"ext"`
}

type claudeStoredDownload struct {
	Version      string `json:"version"`
	Plat         string `json:"plat"`
	PlatformName string `json:"platform_name"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	DownloadedAt string `json:"downloaded_at"`
}

var (
	claudeDownloadsDir = "/app/data/claude-code"
	claudeCache        struct {
		releases  []claudeRelease
		checkedAt time.Time
	}
	claudeCacheMu sync.Mutex
)

// GET /api/admin/claude-code/releases — list available versions + platforms
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

// POST /api/admin/claude-code/download — download specific version + platform
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

		// Validate platform
		valid := false
		var ext string
		for _, p := range claudePlatforms {
			if p.Plat == body.Plat {
				valid = true
				ext = p.Ext
				break
			}
		}
		if !valid {
			writeError(w, "Invalid platform: "+body.Plat, 400)
			return
		}

		// Check if already downloaded
		filename := fmt.Sprintf("claude-code-%s-%s%s", body.Version, body.Plat, ext)
		for _, s := range listStoredDownloads() {
			if s.Filename == filename {
				writeJSON(w, map[string]any{"status": "already_exists", "filename": filename})
				return
			}
		}

		// Download from GCS
		size, err := downloadClaudeBinary(body.Version, body.Plat, ext, filename)
		if err != nil {
			writeError(w, "Download failed: "+err.Error(), 500)
			return
		}

		cleanupOldDownloads(3)

		writeJSON(w, map[string]any{"status": "ok", "filename": filename, "size": size})
	}
}

// GET /api/admin/claude-code/downloads/{filename} — serve binary for browser download
func apiClaudeCodeServeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !strings.HasPrefix(filename, "claude-code-") {
			writeError(w, "Invalid filename", 400)
			return
		}
		path := filepath.Join(claudeDownloadsDir, filename)
		contentType := "application/octet-stream"
		if strings.HasSuffix(filename, ".exe") {
			contentType = "application/vnd.microsoft.portable-executable"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		http.ServeFile(w, r, path)
	}
}

// DELETE /api/admin/claude-code/downloads — delete a stored download
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
		path := filepath.Join(claudeDownloadsDir, body.Filename)
		if err := os.Remove(path); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
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

	// Use GitHub releases API (no auth needed for public repos, 60 req/hour)
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/anthropics/claude-code/releases?per_page=30", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "workwork-admin")

	resp, err := http.DefaultClient.Do(req)
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

func listStoredDownloads() []claudeStoredDownload {
	os.MkdirAll(claudeDownloadsDir, 0755)
	entries, err := os.ReadDir(claudeDownloadsDir)
	if err != nil {
		return nil
	}
	var result []claudeStoredDownload
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "claude-code-") {
			continue
		}
		info, _ := e.Info()
		version, plat, platName := parseStoredFilename(e.Name())
		modTime := ""
		if info != nil {
			modTime = info.ModTime().UTC().Format(time.RFC3339)
		}
		result = append(result, claudeStoredDownload{
			Version:      version,
			Plat:         plat,
			PlatformName: platName,
			Filename:     e.Name(),
			Size:         info.Size(),
			DownloadedAt: modTime,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Version != result[j].Version {
			return compareVersions(result[i].Version, result[j].Version) > 0
		}
		return result[i].Plat < result[j].Plat
	})
	return result
}

func parseStoredFilename(name string) (version, plat, platName string) {
	// claude-code-2.1.132-linux-x64 or claude-code-2.1.132-win32-x64.exe
	rest := strings.TrimPrefix(name, "claude-code-")
	rest = strings.TrimSuffix(rest, ".exe")
	// Find platform suffix
	for _, p := range claudePlatforms {
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

func downloadClaudeBinary(version, plat, ext, filename string) (int64, error) {
	os.MkdirAll(claudeDownloadsDir, 0755)

	url := fmt.Sprintf("%s/%s/%s/claude%s", claudeCodeBaseURL, version, plat, ext)

	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GCS returned %d for %s/%s", resp.StatusCode, version, plat)
	}

	path := filepath.Join(claudeDownloadsDir, filename)
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return 0, err
	}

	written, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		return 0, err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}

	return written, nil
}

func cleanupOldDownloads(maxKeep int) {
	downloads := listStoredDownloads()
	if len(downloads) <= maxKeep {
		return
	}
	for _, d := range downloads[maxKeep:] {
		os.Remove(filepath.Join(claudeDownloadsDir, d.Filename))
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
				}
			}
			nums = append(nums, 0)
			continue
		}
		nums = append(nums, n)
	}
	return nums
}
