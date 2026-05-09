package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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

var githubClient = &http.Client{Timeout: 15 * time.Second}

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
	Version string              `json:"version"`
	Date    string              `json:"date"`
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
	data, err := os.ReadFile(claudeManifestPath)
	if err != nil {
		return m
	}
	json.Unmarshal(data, &m)
	return m
}

func saveDownloadsManifestLocked(m claudeDownloadsManifest) {
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
		// Strip assets for JSON output (the caller only needs version/date)
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
		// Build a set of available platforms from asset names
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

		// Find the asset for this version+plat from cached GitHub releases
		release := findCachedRelease(body.Version)
		if release == nil {
			writeError(w, "Version not found in releases", 404)
			return
		}
		assetURL := ""
		assetSize := int64(0)
		for _, a := range release.Assets {
			if parseAssetPlat(a.Name) == body.Plat {
				assetURL = a.BrowserDownloadURL
				assetSize = a.Size
				break
			}
		}
		if assetURL == "" {
			writeError(w, "No asset for platform "+body.Plat+" in version "+body.Version, 400)
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

		size, checksum, err := downloadClaudeBinary(filename, assetURL, assetSize, func(bytes, total int64) {
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
		sendEvent(map[string]any{"type": "done", "filename": filename, "size": size, "checksum": checksum})
	}
}

// GET /api/admin/claude-code/downloads/{filename}
func apiClaudeCodeServeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !strings.HasPrefix(filename, "claude-code-") ||
			strings.Contains(filename, "..") || strings.Contains(filename, "/") {
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
		claudeDownloadMu.Lock()
		m := readDownloadsManifestLocked()
		var kept []claudeStoredDownload
		for _, d := range m.Downloads {
			if d.Filename != body.Filename {
				kept = append(kept, d)
			}
		}
		m.Downloads = kept
		saveDownloadsManifestLocked(m)
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
	// Strip prefix and known suffixes
	rest := strings.TrimPrefix(name, "claude-")
	for _, suf := range []string{".tar.gz", ".zip"} {
		rest = strings.TrimSuffix(rest, suf)
	}
	// Verify it matches a known platform
	for _, p := range claudePlatforms {
		if rest == p.Plat {
			return p.Plat
		}
	}
	return ""
}

// assetExt returns the file extension for an asset name: ".tar.gz" or ".zip"
func assetExt(name string) string {
	if strings.HasSuffix(name, ".tar.gz") {
		return ".tar.gz"
	}
	if strings.HasSuffix(name, ".zip") {
		return ".zip"
	}
	return ""
}

func listStoredDownloads() []claudeStoredDownload {
	m := loadDownloadsManifest()
	return m.Downloads
}

func parseStoredFilename(name string) (version, plat, platName string) {
	rest := strings.TrimPrefix(name, "claude-code-")
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

func downloadClaudeBinary(filename, assetURL string, assetSize int64, onProgress func(bytes, total int64)) (int64, string, error) {
	os.MkdirAll(claudeDownloadsDir, 0755)

	// Download archive to temp file
	ext := assetExt(filename)
	if ext == "" {
		// Derive from stored filename pattern: claude-code-version-plat
		if strings.Contains(filename, "win32-") {
			ext = ".zip"
		} else {
			ext = ".tar.gz"
		}
	}

	tmpArchive := filepath.Join(claudeDownloadsDir, filename+".download"+ext)
	defer os.Remove(tmpArchive)

	f, err := os.Create(tmpArchive)
	if err != nil {
		return 0, "", err
	}

	resp, err := githubClient.Get(assetURL)
	if err != nil {
		f.Close()
		return 0, "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		f.Close()
		return 0, "", fmt.Errorf("HTTP %d downloading asset", resp.StatusCode)
	}

	total := resp.ContentLength
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			f.Write(buf[:n])
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
			return 0, "", readErr
		}
	}
	f.Close()

	// Extract binary from archive
	binaryPath := filepath.Join(claudeDownloadsDir, filename)
	tmpBinary := binaryPath + ".tmp"
	binarySize, err := extractBinary(tmpArchive, ext, tmpBinary)
	if err != nil {
		return 0, "", fmt.Errorf("extract: %w", err)
	}

	// Compute SHA256 of the extracted binary
	hash := sha256.New()
	bf, err := os.Open(tmpBinary)
	if err != nil {
		return 0, "", err
	}
	io.Copy(hash, bf)
	bf.Close()
	checksum := hex.EncodeToString(hash.Sum(nil))

	if err := os.Rename(tmpBinary, binaryPath); err != nil {
		os.Remove(tmpBinary)
		return 0, "", err
	}

	// Parse version and plat from stored filename
	version, plat, platName := parseStoredFilename(filename)

	// Persist metadata (atomic)
	claudeDownloadMu.Lock()
	m := readDownloadsManifestLocked()
	m.Downloads = append(m.Downloads, claudeStoredDownload{
		Version:      version,
		Plat:         plat,
		PlatformName: platName,
		Filename:     filename,
		Size:         binarySize,
		Checksum:     checksum,
		DownloadedAt: time.Now().UTC().Format(time.RFC3339),
	})
	saveDownloadsManifestLocked(m)
	claudeDownloadMu.Unlock()

	return binarySize, checksum, nil
}

func extractBinary(archivePath, ext, outputPath string) (int64, error) {
	switch ext {
	case ".tar.gz":
		return extractTarGz(archivePath, outputPath)
	case ".zip":
		return extractZip(archivePath, outputPath)
	default:
		return 0, fmt.Errorf("unknown archive format: %s", ext)
	}
}

func extractTarGz(archivePath, outputPath string) (int64, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		if hdr.Typeflag == tar.TypeReg {
			out, err := os.Create(outputPath)
			if err != nil {
				return 0, err
			}
			n, err := io.Copy(out, tr)
			out.Close()
			if err != nil {
				return 0, err
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("no binary found in tar.gz")
}

func extractZip(archivePath, outputPath string) (int64, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return 0, err
		}
		out, err := os.Create(outputPath)
		if err != nil {
			rc.Close()
			return 0, err
		}
		n, err := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("no binary found in zip")
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
	saveDownloadsManifestLocked(m)
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
