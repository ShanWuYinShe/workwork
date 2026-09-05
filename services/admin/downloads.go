package main

// downloads.go 是 claude.go / xray.go 的共享下载层。
// 此前两文件约 500 行结构性重复（manifest 读写、下载循环、NDJSON 进度、
// serve/delete 校验、版本比较、过期清理各自成对复制），任何修复都得改两处。
// 两边的 release / stored 条目 JSON 形状完全一致，因此共享层直接用具体类型，
// 差异点收敛为一个描述符：
//
//   - repo URL / per_page（GitHub releases 拉取）
//   - 平台表与资产名→平台解析规则
//   - 落盘文件名模式
//   - 存储目录 / manifest 路径 / 锁 / 保留份数
//   - 是否保留 SHASUMS256 资产（claude 用于校验）、是否丢弃无资产的 release
//
// HTTP handler 仍留在各自文件中（API 路由、文件名前缀语义、Content-Type 等
// 是各页面接口契约），机制函数统一走本文件。

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

// githubClient / downloadClient 供两个下载面共用（原各文件顶层变量）。
var (
	githubClient   = &http.Client{Timeout: 15 * time.Second}
	downloadClient = &http.Client{Timeout: 10 * time.Minute}
)

// ghAsset 是 GitHub release asset 的通用形状（此前 claudeReleaseAsset /
// xrayReleaseAsset 成对重复）。
type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// ghRelease 是 GitHub releases API 的通用响应条目。
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

// releaseInfo 是内部版本缓存条目（此前 claudeRelease / xrayRelease 成对重复，
// 字段与 JSON 标签完全一致）。
type releaseInfo struct {
	Version string    `json:"version"`
	Date    string    `json:"date"`
	Assets  []ghAsset `json:"-"`
}

// downloadPlatform 是平台表条目（Label 面向 UI，Plat 是资产名标识）。
type downloadPlatform struct {
	Label string `json:"label"`
	Plat  string `json:"plat"`
}

// storedDownload 是 manifest 条目（此前 claudeStoredDownload /
// xrayStoredDownload 成对重复）。两个 /stored 端点与前端消费的 JSON 字段
// 与此完全一致，无需保留两份类型。
type storedDownload struct {
	Version      string `json:"version"`
	Plat         string `json:"plat"`
	PlatformName string `json:"platform_name"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	Checksum     string `json:"checksum"`
	DownloadedAt string `json:"downloaded_at"`
}

type storedManifest struct {
	Downloads []storedDownload `json:"downloads"`
}

// downloadService 聚合一个工具（claude / xray）的全部下载管理机制。
type downloadService struct {
	// GitHub releases 列表 API 与条数上限。
	repoURL string
	perPage int

	// 平台表与资产名→平台解析（返回 "" 表示该资产与平台无关）。
	platforms      []downloadPlatform
	parseAssetPlat func(name string) string

	// 是否把 SHASUMS256.txt 一并纳入 assets（claude 需要校验文件；xray 不需要）。
	keepChecksumsFile bool
	// 是否丢弃无有效资产的 release（xray 跳过空 release，claude 保留）。
	dropEmptyReleases bool

	// 落盘文件名：由 version+plat+资产名推导（两者扩展名规则不同）。
	buildFilename func(version, plat, assetName string) string

	// serve/delete 的文件名前缀白名单（claude-" / "Xray-"+"xray-"）。
	filenamePrefixes []string

	dir          string
	manifestPath string
	mu           sync.Mutex

	cache struct {
		mu        sync.Mutex
		fetchMu   sync.Mutex // singleflight：合并缓存过期时的并发回源，防打爆 GitHub 匿名限额
		releases  []releaseInfo
		checkedAt time.Time
	}

	// 下载完成后保留的最近份数（claude 10 / xray 5）。
	maxKeep int
}

func (s *downloadService) loadManifest() storedManifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var m storedManifest
	loadJSONFile(s.manifestPath, &m)
	return m
}

func (s *downloadService) saveManifestLocked(m storedManifest) error {
	return saveJSONFile(s.manifestPath, m)
}

func (s *downloadService) listStored() []storedDownload {
	return s.loadManifest().Downloads
}

func (s *downloadService) platformLabel(plat string) string {
	for _, p := range s.platforms {
		if p.Plat == plat {
			return p.Label
		}
	}
	return ""
}

func (s *downloadService) validPlatform(plat string) bool {
	return s.platformLabel(plat) != ""
}

func (s *downloadService) findCachedRelease(version string) *releaseInfo {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	for i := range s.cache.releases {
		if s.cache.releases[i].Version == version {
			cp := s.cache.releases[i]
			return &cp
		}
	}
	return nil
}

func (s *downloadService) invalidateCache() {
	s.cache.mu.Lock()
	s.cache.checkedAt = time.Time{}
	s.cache.mu.Unlock()
}

// fetchVersions 拉取并排序 GitHub releases（带 30min 缓存与并发合并）。
// fetchMu + double-check：只有一个请求回源，其余等锁后命中新缓存——不加锁时
// 缓存过期的瞬间所有并发请求都直击 GitHub API，打爆 60/h 匿名限额。
func (s *downloadService) fetchVersions() ([]releaseInfo, error) {
	s.cache.mu.Lock()
	if time.Since(s.cache.checkedAt) < 30*time.Minute && len(s.cache.releases) > 0 {
		releases := s.cache.releases
		s.cache.mu.Unlock()
		return releases, nil
	}
	s.cache.mu.Unlock()

	s.cache.fetchMu.Lock()
	defer s.cache.fetchMu.Unlock()
	s.cache.mu.Lock()
	if time.Since(s.cache.checkedAt) < 30*time.Minute && len(s.cache.releases) > 0 {
		releases := s.cache.releases
		s.cache.mu.Unlock()
		return releases, nil
	}
	s.cache.mu.Unlock()

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s?per_page=%d", s.repoURL, s.perPage), nil)
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

	var ghReleases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&ghReleases); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}

	releases := make([]releaseInfo, 0, len(ghReleases))
	for _, gr := range ghReleases {
		v := strings.TrimPrefix(gr.TagName, "v")
		rel := releaseInfo{Version: v, Date: gr.PublishedAt}
		for _, a := range gr.Assets {
			if s.parseAssetPlat(a.Name) != "" || (s.keepChecksumsFile && a.Name == "SHASUMS256.txt") {
				rel.Assets = append(rel.Assets, a)
			}
		}
		if s.dropEmptyReleases && len(rel.Assets) == 0 {
			continue
		}
		releases = append(releases, rel)
	}
	sort.Slice(releases, func(i, j int) bool {
		return compareVersions(releases[i].Version, releases[j].Version) > 0
	})

	s.cache.mu.Lock()
	s.cache.releases = releases
	s.cache.checkedAt = time.Now()
	s.cache.mu.Unlock()

	return releases, nil
}

// downloadArchive 流式下载资产到 s.dir：Content-Length 预检 + 流式累计双重
// 大小限制，SHA-256 校验和随流计算，原子 rename 落盘，manifest 去重追加。
func (s *downloadService) downloadArchive(parentCtx context.Context, filename, version, plat, assetURL string, onProgress func(bytes, total int64)) (int64, string, error) {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return 0, "", err
	}

	f, err := os.CreateTemp(s.dir, filename+".download-*")
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

	finalPath := filepath.Join(s.dir, filename)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return 0, "", err
	}

	s.mu.Lock()
	m := s.loadManifest()
	// 防止并发下载同一 version+plat 导致 manifest 重复条目（文件因 rename 已覆盖一致）。
	dup := false
	for _, d := range m.Downloads {
		if d.Filename == filename {
			dup = true
			break
		}
	}
	if !dup {
		m.Downloads = append(m.Downloads, storedDownload{
			Version:      version,
			Plat:         plat,
			PlatformName: s.platformLabel(plat),
			Filename:     filename,
			Size:         written,
			Checksum:     checksum,
			DownloadedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err := s.saveManifestLocked(m); err != nil {
			s.mu.Unlock()
			return 0, "", fmt.Errorf("persist downloads manifest: %w", err)
		}
	}
	s.mu.Unlock()

	return written, checksum, nil
}

// cleanupOld 保留最近 maxKeep 条，淘汰更早的条目及其文件。
func (s *downloadService) cleanupOld() {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadManifest()
	if len(m.Downloads) <= s.maxKeep {
		return
	}
	// 保留最近 maxKeep 条（列表按下载时间追加，末尾最新），淘汰最早的多余条目。
	// 此前反向清理会把刚下载完的文件立即删掉，导致“下载成功但文件不存在”。
	drop := len(m.Downloads) - s.maxKeep
	kept := keepNewestDownloads(m.Downloads, s.maxKeep)
	for _, d := range m.Downloads[:drop] {
		os.Remove(filepath.Join(s.dir, d.Filename))
	}
	m.Downloads = kept
	if err := s.saveManifestLocked(m); err != nil {
		log.Printf("ERROR: save downloads manifest (cleanup) %s: %v", s.manifestPath, err)
	}
}

// deleteStored 删除文件并从 manifest 移除。返回 persist 错误（供上层
// writeInternalError）与文件删除错误。
func (s *downloadService) deleteStored(filename string) error {
	if err := os.Remove(filepath.Join(s.dir, filename)); err != nil {
		return err
	}
	s.mu.Lock()
	m := s.loadManifest()
	var kept []storedDownload
	for _, d := range m.Downloads {
		if d.Filename != filename {
			kept = append(kept, d)
		}
	}
	m.Downloads = kept
	if err := s.saveManifestLocked(m); err != nil {
		s.mu.Unlock()
		log.Printf("ERROR: save downloads manifest (delete) %s: %v", s.manifestPath, err)
		return fmt.Errorf("persist downloads manifest: %w", err)
	}
	s.mu.Unlock()
	return nil
}

// validStoredFilename 校验 serve/delete 的文件名：前缀白名单（xray 额外接受
// 历史小写 "xray-"）+ 拒绝路径穿越/分隔符/临时文件标记。
func validStoredFilename(filename string, prefixes ...string) bool {
	okPrefix := false
	for _, p := range prefixes {
		if strings.HasPrefix(filename, p) {
			okPrefix = true
			break
		}
	}
	if !okPrefix {
		return false
	}
	return !strings.Contains(filename, "..") &&
		!strings.Contains(filename, "/") &&
		!strings.Contains(filename, ".download-")
}

// keepNewestDownloads 返回最近 maxKeep 条下载记录（含拷贝，避免与入参共享底层数组）。
func keepNewestDownloads(entries []storedDownload, maxKeep int) []storedDownload {
	if len(entries) <= maxKeep {
		return append([]storedDownload{}, entries...)
	}
	drop := len(entries) - maxKeep
	return append([]storedDownload{}, entries[drop:]...)
}

// compareVersions 语义化版本比较：逐段数值比较，段数少者小。
// 此前 claude.go / xray.go 各有一份完全相同的实现。
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

// joinDownloadPath 拼接下载目录与文件名（ ServeFile 用；文件名已经
// validStoredFilename 白名单校验）。
func joinDownloadPath(dir, filename string) string {
	return filepath.Join(dir, filename)
}

// downloadDirs 返回全部工具下载目录（purgeStaleDownloadTemps 启动清理用）。
func downloadDirs() []string {
	return []string{claudeDownloads.dir, xrayDownloads.dir}
}
