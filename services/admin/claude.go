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

// Claude Code release info from npm registry
type claudeRelease struct {
	Version string `json:"version"`
	Date    string `json:"date"`
}

type claudeStoredDownload struct {
	Version      string `json:"version"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	DownloadedAt string `json:"downloaded_at"`
}

var (
	claudeDownloadsDir = "/app/data/claude-code"
	claudeCache        struct {
		releases []claudeRelease
		checkedAt time.Time
	}
	claudeCacheMu sync.Mutex
)

// GET /api/admin/claude-code/releases — list available versions from npm
func apiClaudeCodeReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releases, err := fetchClaudeCodeVersions()
		if err != nil {
			writeError(w, "Failed to fetch versions: "+err.Error(), 500)
			return
		}
		stored := listStoredDownloads()
		writeJSON(w, map[string]any{
			"releases": releases,
			"stored":   stored,
		})
	}
}

// POST /api/admin/claude-code/download — download a specific version
func apiClaudeCodeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Version == "" {
			writeError(w, "Version required", 400)
			return
		}

		// Check if already downloaded
		for _, s := range listStoredDownloads() {
			if s.Version == body.Version {
				writeJSON(w, map[string]any{"status": "already_exists", "filename": s.Filename})
				return
			}
		}

		// Download from npm registry
		filename, size, err := downloadClaudeVersion(body.Version)
		if err != nil {
			writeError(w, "Download failed: "+err.Error(), 500)
			return
		}

		// Cleanup old versions (keep max 3)
		cleanupOldDownloads(3)

		writeJSON(w, map[string]any{"status": "ok", "filename": filename, "size": size})
	}
}

// GET /api/admin/claude-code/downloads/{filename} — serve stored tarball for browser download
func apiClaudeCodeServeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !strings.HasPrefix(filename, "claude-code-") || !strings.HasSuffix(filename, ".tgz") {
			writeError(w, "Invalid filename", 400)
			return
		}
		path := filepath.Join(claudeDownloadsDir, filename)
		w.Header().Set("Content-Type", "application/gzip")
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
		// Sanitize: only allow filenames matching claude-code-*.tgz
		if !strings.HasPrefix(body.Filename, "claude-code-") || !strings.HasSuffix(body.Filename, ".tgz") {
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

	resp, err := http.Get("https://registry.npmjs.org/@anthropic-ai/claude-code")
	if err != nil {
		return nil, fmt.Errorf("npm registry: %w", err)
	}
	defer resp.Body.Close()

	var meta struct {
		Versions map[string]struct {
			Version string `json:"version"`
		} `json:"versions"`
		Time map[string]string `json:"time"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}

	var releases []claudeRelease
	for v := range meta.Versions {
		date := meta.Time[v]
		if date == "" {
			continue
		}
		releases = append(releases, claudeRelease{Version: v, Date: date})
	}
	// Sort by version descending (simple semver-ish)
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
		if e.IsDir() || !strings.HasPrefix(e.Name(), "claude-code-") || !strings.HasSuffix(e.Name(), ".tgz") {
			continue
		}
		info, _ := e.Info()
		version := strings.TrimPrefix(e.Name(), "claude-code-")
		version = strings.TrimSuffix(version, ".tgz")
		// Handle scoped package format: @anthropic-ai/claude-code → just version
		// But our tarball always uses plain name
		modTime := ""
		if info != nil {
			modTime = info.ModTime().UTC().Format(time.RFC3339)
		}
		result = append(result, claudeStoredDownload{
			Version:      version,
			Filename:     e.Name(),
			Size:         info.Size(),
			DownloadedAt: modTime,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return compareVersions(result[i].Version, result[j].Version) > 0
	})
	return result
}

func downloadClaudeVersion(version string) (string, int64, error) {
	os.MkdirAll(claudeDownloadsDir, 0755)

	// npm registry tarball URL
	// @anthropic-ai/claude-code@2.1.131 → /@anthropic-ai/claude-code/-/claude-code-2.1.131.tgz
	filename := fmt.Sprintf("claude-code-%s.tgz", version)
	url := fmt.Sprintf("https://registry.npmjs.org/@anthropic-ai/claude-code/-/%s", filename)

	resp, err := http.Get(url)
	if err != nil {
		return "", 0, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("npm returned %d", resp.StatusCode)
	}

	path := filepath.Join(claudeDownloadsDir, filename)
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return "", 0, err
	}

	written, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		return "", 0, err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return "", 0, err
	}

	return filename, written, nil
}

func cleanupOldDownloads(maxKeep int) {
	downloads := listStoredDownloads()
	if len(downloads) <= maxKeep {
		return
	}
	// Already sorted by version desc, so keep first maxKeep
	for _, d := range downloads[maxKeep:] {
		os.Remove(filepath.Join(claudeDownloadsDir, d.Filename))
	}
}

// compareVersions returns >0 if a > b, <0 if a < b, 0 if equal
func compareVersions(a, b string) int {
	ap := parseVersionParts(a)
	bp := parseVersionParts(b)
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] != bp[i] {
			return ap[i] - bp[i]
		}
	}
	return len(ap) - len(bp)
}

func parseVersionParts(v string) []int {
	// Strip leading 'v' if present
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	var nums []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			// Handle pre-release suffixes like "2.0.0-beta.1"
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
