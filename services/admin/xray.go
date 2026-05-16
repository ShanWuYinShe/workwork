package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	data, err := os.ReadFile(xrayManifestPath)
	if err != nil {
		return m
	}
	json.Unmarshal(data, &m)
	return m
}

func saveXrayManifestLocked(m xrayDownloadsManifest) {
	os.MkdirAll(xrayDownloadsDir, 0755)
	data, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(xrayManifestPath, data, 0644)
}

// GET /api/admin/xray/releases
func apiXrayReleases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		releases, err := fetchXrayVersions()
		if err != nil {
			writeError(w, "Failed to fetch versions: "+err.Error(), 500)
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
			writeError(w, "Failed to refresh: "+err.Error(), 500)
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Version == "" || body.Plat == "" {
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

		filename := fmt.Sprintf("xray-%s-%s", body.Version, body.Plat)
		for _, s := range listXrayStored() {
			if s.Filename == filename {
				writeJSON(w, map[string]any{"status": "already_exists", "filename": filename})
				return
			}
		}

		release := findXrayCachedRelease(body.Version)
		if release == nil {
			writeError(w, "Version not found in releases", 404)
			return
		}
		assetURL := ""
		for _, a := range release.Assets {
			if parseXrayAssetPlat(a.Name) == body.Plat {
				assetURL = a.BrowserDownloadURL
				break
			}
		}
		if assetURL == "" {
			writeError(w, "No asset for platform "+body.Plat+" in version "+body.Version, 400)
			return
		}

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

		var lastPct int64 = -1
		size, checksum, err := downloadXrayBinary(filename, assetURL, func(bytes, total int64) {
			if total <= 0 {
				return
			}
			pct := bytes * 100 / total
			if pct >= lastPct+5 || bytes >= total {
				lastPct = pct
				sendEvent(map[string]any{"type": "progress", "bytes": bytes, "total": total, "percent": pct})
			}
		})
		if err != nil {
			sendEvent(map[string]any{"type": "error", "error": err.Error()})
			return
		}

		cleanupOldXrayDownloads(5)
		sendEvent(map[string]any{"type": "done", "filename": filename, "size": size, "checksum": checksum})
	}
}

// GET /api/admin/xray/downloads/{filename}
func apiXrayServeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !strings.HasPrefix(filename, "xray-") ||
			strings.Contains(filename, "..") || strings.Contains(filename, "/") {
			writeError(w, "Invalid filename", 400)
			return
		}
		path := filepath.Join(xrayDownloadsDir, filename)
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Filename == "" {
			writeError(w, "Filename required", 400)
			return
		}
		if !strings.HasPrefix(body.Filename, "xray-") ||
			strings.Contains(body.Filename, "..") || strings.Contains(body.Filename, "/") {
			writeError(w, "Invalid filename", 400)
			return
		}
		if err := os.Remove(filepath.Join(xrayDownloadsDir, body.Filename)); err != nil {
			writeError(w, err.Error(), 500)
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
		saveXrayManifestLocked(m)
		xrayDownloadMu.Unlock()
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

func fetchXrayVersions() ([]xrayRelease, error) {
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
	rest := strings.TrimPrefix(name, "xray-")
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

func downloadXrayBinary(filename, assetURL string, onProgress func(bytes, total int64)) (int64, string, error) {
	os.MkdirAll(xrayDownloadsDir, 0755)

	tmpZip := filepath.Join(xrayDownloadsDir, filename+".download.zip")
	defer os.Remove(tmpZip)

	f, err := os.Create(tmpZip)
	if err != nil {
		return 0, "", err
	}

	req, err := http.NewRequest("GET", assetURL, nil)
	if err != nil {
		f.Close()
		return 0, "", fmt.Errorf("download request: %w", err)
	}
	resp, err := githubClient.Do(req)
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
	buf := make([]byte, 128*1024)
	var written int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return 0, "", fmt.Errorf("write: %w", err)
			}
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
	if err := f.Close(); err != nil {
		return 0, "", fmt.Errorf("close: %w", err)
	}

	// Extract xray binary from zip
	binaryPath := filepath.Join(xrayDownloadsDir, filename)
	tmpBinary := binaryPath + ".tmp"
	binarySize, err := extractXrayFromZip(tmpZip, tmpBinary)
	if err != nil {
		return 0, "", fmt.Errorf("extract: %w", err)
	}

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

	version, plat, platName := parseXrayStoredFilename(filename)

	xrayDownloadMu.Lock()
	m := readXrayManifestLocked()
	m.Downloads = append(m.Downloads, xrayStoredDownload{
		Version:      version,
		Plat:         plat,
		PlatformName: platName,
		Filename:     filename,
		Size:         binarySize,
		Checksum:     checksum,
		DownloadedAt: time.Now().UTC().Format(time.RFC3339),
	})
	saveXrayManifestLocked(m)
	xrayDownloadMu.Unlock()

	return binarySize, checksum, nil
}

func extractXrayFromZip(zipPath, outputPath string) (int64, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	// Xray binary is typically named "xray" in the zip
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.ToLower(filepath.Base(f.Name))
		if name == "xray" || name == "xray.exe" {
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
			return n, err
		}
	}
	// Fallback: take the first regular file
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
		return n, err
	}
	return 0, fmt.Errorf("no binary found in zip")
}

func cleanupOldXrayDownloads(maxKeep int) {
	xrayDownloadMu.Lock()
	defer xrayDownloadMu.Unlock()
	m := readXrayManifestLocked()
	if len(m.Downloads) <= maxKeep {
		return
	}
	for _, d := range m.Downloads[maxKeep:] {
		os.Remove(filepath.Join(xrayDownloadsDir, d.Filename))
	}
	m.Downloads = m.Downloads[:maxKeep]
	saveXrayManifestLocked(m)
}
