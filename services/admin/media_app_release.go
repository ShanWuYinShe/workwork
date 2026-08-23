package main

import (
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

const maxMediaAppAPKSize = 300 << 20 // 300 MiB

// mediaAppReleaseMu 串行化发布历史的"读-改-写"序列（发布追加 / 回滚 / 删除），
// 避免并发操作互相覆盖丢记录。单个文件本身的完整性由 atomicWriteFile 保证，
// 锁保证跨文件（manifest + history）的一致性窗口。
var mediaAppReleaseMu sync.Mutex

type mediaAppReleaseManifest struct {
	VersionCode  int    `json:"versionCode"`
	VersionName  string `json:"versionName"`
	APKURL       string `json:"apkUrl"`
	APKFilename  string `json:"apkFilename,omitempty"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
	ReleaseNotes string `json:"releaseNotes,omitempty"`
	PublishedAt  string `json:"publishedAt,omitempty"`
}

func apiMediaAppReleaseGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		variant := strings.TrimSpace(r.URL.Query().Get("variant"))
		manifest, exists, err := readMediaAppReleaseManifest(variant)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		apkPath := mediaAppReleaseAPKPath(manifest, variant)
		info, statErr := os.Stat(apkPath)
		apkExists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			writeInternalError(w, statErr)
			return
		}
		writeJSON(w, map[string]any{
			"published": exists && apkExists,
			"manifest":  manifest,
			"apkSizeBytes": func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}(),
		})
	}
}

// releaseHistoryEntry 是版本历史列表的一行：manifest 字段平铺 + 状态标注。
type releaseHistoryEntry struct {
	mediaAppReleaseManifest
	// Current 表示该记录是当前对外发布的版本（versionCode 与 sha256 双重匹配）。
	Current bool `json:"current"`
	// ApkExists 表示该版本的 APK 文件仍在磁盘上（可下载/可回滚）。
	ApkExists bool `json:"apkExists"`
}

// GET /api/admin/media-center/app-release/history?variant=X
func apiMediaAppReleaseHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		variant := strings.TrimSpace(r.URL.Query().Get("variant"))
		mediaAppReleaseMu.Lock()
		defer mediaAppReleaseMu.Unlock()

		current, currentExists, err := readMediaAppReleaseManifest(variant)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		records, err := readMediaAppReleaseHistory(variant)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		entries := make([]releaseHistoryEntry, 0, len(records))
		for _, rec := range records {
			apkPath := mediaAppReleaseAPKPath(rec, variant)
			_, statErr := os.Stat(apkPath)
			if statErr != nil && !os.IsNotExist(statErr) {
				writeInternalError(w, statErr)
				return
			}
			entries = append(entries, releaseHistoryEntry{
				mediaAppReleaseManifest: rec,
				Current: currentExists &&
					rec.VersionCode == current.VersionCode &&
					strings.EqualFold(rec.SHA256, current.SHA256),
				ApkExists: statErr == nil,
			})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].VersionCode > entries[j].VersionCode
		})
		writeJSON(w, map[string]any{"versions": entries, "count": len(entries)})
	}
}

// POST /api/admin/media-center/app-release/rollback — body: {variant, versionCode}
// 把当前发布指针切回历史中的某个版本。已安装更高版本的客户端不会降级
// （客户端按 versionCode 单调比较），回滚只影响尚未升级的设备。
func apiMediaAppReleaseRollback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variant     string `json:"variant"`
			VersionCode int    `json:"versionCode"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "请求格式不正确", http.StatusBadRequest)
			return
		}
		variant := sanitizeVariant(body.Variant)
		if body.VersionCode <= 0 {
			writeError(w, "versionCode 必须是正整数", http.StatusBadRequest)
			return
		}

		mediaAppReleaseMu.Lock()
		defer mediaAppReleaseMu.Unlock()

		current, currentExists, err := readMediaAppReleaseManifest(variant)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if currentExists && current.VersionCode == body.VersionCode {
			writeError(w, "该版本已是当前发布版本", http.StatusBadRequest)
			return
		}
		target, ok, err := findHistoryRecord(variant, body.VersionCode)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if !ok {
			writeError(w, "版本历史中不存在该 versionCode", http.StatusNotFound)
			return
		}
		apkPath := mediaAppReleaseAPKPath(target, variant)
		info, statErr := os.Stat(apkPath)
		// 非 ENOENT 的 IO 错误(权限/磁盘)按内部错误处理并记日志，
		// 不能伪装成业务 409 把真实故障吞掉。
		if statErr != nil && !os.IsNotExist(statErr) {
			writeInternalError(w, statErr)
			return
		}
		if statErr != nil || info.Size() != target.SizeBytes {
			writeError(w, "该版本的 APK 文件已不存在或大小不符，无法回滚", http.StatusConflict)
			return
		}
		if err := writeMediaAppReleaseManifest(target, variant); err != nil {
			writeInternalError(w, err)
			return
		}
		log.Printf("app release rollback: variant=%q versionCode=%d", variant, target.VersionCode)
		writeJSON(w, map[string]any{"manifest": target})
	}
}

// DELETE /api/admin/media-center/app-release/history — body: {variant, versionCode}
// 从历史中移除一条记录并尽力删除其 APK 文件。当前正在发布的版本不允许删除。
func apiMediaAppReleaseVersionDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variant     string `json:"variant"`
			VersionCode int    `json:"versionCode"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "请求格式不正确", http.StatusBadRequest)
			return
		}
		variant := sanitizeVariant(body.Variant)
		if body.VersionCode <= 0 {
			writeError(w, "versionCode 必须是正整数", http.StatusBadRequest)
			return
		}

		mediaAppReleaseMu.Lock()
		defer mediaAppReleaseMu.Unlock()

		current, currentExists, err := readMediaAppReleaseManifest(variant)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if currentExists && current.VersionCode == body.VersionCode {
			writeError(w, "不能删除当前正在发布的版本；请先发布新版本后再清理", http.StatusBadRequest)
			return
		}

		records, err := readMediaAppReleaseHistory(variant)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		filtered := make([]mediaAppReleaseManifest, 0, len(records))
		removed := false
		for _, rec := range records {
			if rec.VersionCode == body.VersionCode {
				removed = true
				continue
			}
			filtered = append(filtered, rec)
		}
		if !removed {
			writeError(w, "版本历史中不存在该 versionCode", http.StatusNotFound)
			return
		}
		if err := writeMediaAppReleaseHistory(variant, filtered); err != nil {
			writeInternalError(w, err)
			return
		}
		// 被删记录引用的 APK 若不再被任何 manifest/历史引用，会被 prune 清理。
		pruneOrphanAppAPKs()
		log.Printf("app release history delete: variant=%q versionCode=%d remaining=%d", variant, body.VersionCode, len(filtered))
		writeJSON(w, map[string]any{"removed": true, "remaining": len(filtered)})
	}
}

// GET /api/admin/media-center/app-release/download?variant=&versionCode=
// 把指定版本（缺省为当前发布版本）的 APK 流式发给管理员浏览器。
func apiMediaAppReleaseDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		variant := sanitizeVariant(strings.TrimSpace(r.URL.Query().Get("variant")))
		versionRaw := strings.TrimSpace(r.URL.Query().Get("versionCode"))

		var manifest mediaAppReleaseManifest
		found := false
		if versionRaw == "" {
			m, exists, err := readMediaAppReleaseManifest(variant)
			if err != nil {
				writeInternalError(w, err)
				return
			}
			manifest, found = m, exists
		} else {
			code, parseErr := strconv.Atoi(versionRaw)
			if parseErr != nil || code <= 0 {
				writeError(w, "versionCode 必须是正整数", http.StatusBadRequest)
				return
			}
			mediaAppReleaseMu.Lock()
			rec, ok, err := findHistoryRecord(variant, code)
			mediaAppReleaseMu.Unlock()
			if err != nil {
				writeInternalError(w, err)
				return
			}
			if ok {
				manifest, found = rec, true
			} else if m, exists, mErr := readMediaAppReleaseManifest(variant); mErr == nil && exists && m.VersionCode == code {
				// 兜底：历史功能上线前的旧发布没有历史记录，但当前 manifest 匹配。
				manifest, found = m, true
			}
		}
		if !found {
			writeError(w, "未找到该版本的发布记录", http.StatusNotFound)
			return
		}

		apkPath := mediaAppReleaseAPKPath(manifest, variant)
		f, err := os.Open(apkPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, "该版本的 APK 文件不存在", http.StatusNotFound)
				return
			}
			writeInternalError(w, err)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			writeInternalError(w, err)
			return
		}

		filename := strings.TrimSpace(manifest.APKFilename)
		if filename == "" {
			filename = fmt.Sprintf("media-center-app-v%d.apk", manifest.VersionCode)
		}
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHeaderValue(filename)))
		http.ServeContent(w, r, filename, info.ModTime(), f)
	}
}

func apiMediaAppReleaseUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxMediaAppAPKSize+2<<20)
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			writeError(w, "上传内容过大或格式不正确", http.StatusBadRequest)
			return
		}
		// 超过内存阈值的 multipart 部分会落盘为临时文件，请求结束必须显式清理，
		// 否则每个 APK 上传都会在容器临时目录留下最多数百 MB 的残留文件。
		defer r.MultipartForm.RemoveAll()

		variant := strings.TrimSpace(r.FormValue("variant"))
		apkPath := mediaCenterAppUpdateAPKPath(variant)

		versionCode, err := strconv.Atoi(strings.TrimSpace(r.FormValue("versionCode")))
		if err != nil || versionCode <= 0 {
			writeError(w, "versionCode 必须是正整数", http.StatusBadRequest)
			return
		}
		versionName := strings.TrimSpace(r.FormValue("versionName"))
		if versionName == "" || len(versionName) > 64 {
			writeError(w, "versionName 不能为空且不能超过 64 字符", http.StatusBadRequest)
			return
		}
		releaseNotes := strings.TrimSpace(r.FormValue("releaseNotes"))
		if len(releaseNotes) > 4000 {
			writeError(w, "releaseNotes 不能超过 4000 字符", http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("apk")
		if err != nil {
			writeError(w, "请上传 apk 文件", http.StatusBadRequest)
			return
		}
		defer file.Close()
		if !strings.EqualFold(filepath.Ext(header.Filename), ".apk") {
			writeError(w, "只允许上传 .apk 文件", http.StatusBadRequest)
			return
		}

		if err := os.MkdirAll(filepath.Dir(apkPath), 0755); err != nil {
			writeInternalError(w, err)
			return
		}
		tmp, err := os.CreateTemp(filepath.Dir(apkPath), ".media-center-app.apk.tmp-*")
		if err != nil {
			writeInternalError(w, err)
			return
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)

		hasher := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(file, maxMediaAppAPKSize+1))
		if closeErr := tmp.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			writeInternalError(w, copyErr)
			return
		}
		if written <= 0 {
			writeError(w, "apk 文件不能为空", http.StatusBadRequest)
			return
		}
		if written > maxMediaAppAPKSize {
			writeError(w, fmt.Sprintf("apk 文件不能超过 %d MiB", maxMediaAppAPKSize>>20), http.StatusBadRequest)
			return
		}
		checksum := hex.EncodeToString(hasher.Sum(nil))
		if err := os.Chmod(tmpPath, 0644); err != nil {
			writeInternalError(w, err)
			return
		}
		apkFilename := mediaAppReleaseFilename(versionCode, checksum)
		finalAPKPath := filepath.Join(filepath.Dir(apkPath), apkFilename)
		if err := os.Rename(tmpPath, finalAPKPath); err != nil {
			writeInternalError(w, err)
			return
		}

		manifest := mediaAppReleaseManifest{
			VersionCode:  versionCode,
			VersionName:  versionName,
			APKURL:       "/app/download",
			APKFilename:  apkFilename,
			SizeBytes:    written,
			SHA256:       checksum,
			ReleaseNotes: releaseNotes,
			PublishedAt:  time.Now().UTC().Format(time.RFC3339),
		}
		// manifest 写盘 + 历史追加 + 孤儿 APK 清理在同一临界区内完成：并发上传时
		// 后写者整体胜出，且 prune 读到的引用集合不会缺刚发布的文件。
		mediaAppReleaseMu.Lock()
		defer mediaAppReleaseMu.Unlock()
		if err := writeMediaAppReleaseManifest(manifest, variant); err != nil {
			writeInternalError(w, err)
			return
		}
		if err := upsertMediaAppReleaseHistory(variant, manifest); err != nil {
			writeInternalError(w, err)
			return
		}
		// 尽力清理不再被任何变体 manifest/历史引用的历史 APK,避免确定性文件名无限堆积。
		pruneOrphanAppAPKs(filepath.Base(finalAPKPath))
		log.Printf("app release publish: variant=%q versionCode=%d size=%d", variant, versionCode, written)
		writeJSON(w, map[string]any{
			"manifest": manifest,
		})
	}
}

func readMediaAppReleaseManifest(variant string) (mediaAppReleaseManifest, bool, error) {
	data, err := os.ReadFile(mediaCenterAppUpdateManifestPath(variant))
	if err != nil {
		if os.IsNotExist(err) {
			return mediaAppReleaseManifest{}, false, nil
		}
		return mediaAppReleaseManifest{}, false, err
	}
	var manifest mediaAppReleaseManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return mediaAppReleaseManifest{}, false, err
	}
	return manifest, true, nil
}

func writeMediaAppReleaseManifest(manifest mediaAppReleaseManifest, variant string) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(mediaCenterAppUpdateManifestPath(variant), data, 0644)
}

// readMediaAppReleaseHistory 读取变体的发布历史。文件不存在视为空历史；
// JSON 损坏返回错误（调用方按内部错误处理，绝不静默清空历史）。
func readMediaAppReleaseHistory(variant string) ([]mediaAppReleaseManifest, error) {
	data, err := os.ReadFile(mediaCenterAppUpdateHistoryPath(variant))
	if err != nil {
		if os.IsNotExist(err) {
			return []mediaAppReleaseManifest{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return []mediaAppReleaseManifest{}, nil
	}
	var records []mediaAppReleaseManifest
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse app release history %q: %w", filepath.Base(mediaCenterAppUpdateHistoryPath(variant)), err)
	}
	return records, nil
}

func writeMediaAppReleaseHistory(variant string, records []mediaAppReleaseManifest) error {
	sort.Slice(records, func(i, j int) bool {
		return records[i].VersionCode > records[j].VersionCode
	})
	if records == nil {
		records = []mediaAppReleaseManifest{}
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(mediaCenterAppUpdateHistoryPath(variant), data, 0644)
}

// upsertMediaAppReleaseHistory 追加（或按 versionCode 覆盖）一条发布记录并落盘。
func upsertMediaAppReleaseHistory(variant string, record mediaAppReleaseManifest) error {
	records, err := readMediaAppReleaseHistory(variant)
	if err != nil {
		return err
	}
	return writeMediaAppReleaseHistory(variant, upsertReleaseRecord(records, record))
}

// upsertReleaseRecord 按 versionCode 插入/替换记录并保持 versionCode 降序。
// 同一 versionCode 重复发布时以最新一次为准（APK 文件名含内容哈希，天然区分）。
func upsertReleaseRecord(records []mediaAppReleaseManifest, record mediaAppReleaseManifest) []mediaAppReleaseManifest {
	replaced := false
	out := make([]mediaAppReleaseManifest, 0, len(records)+1)
	for _, existing := range records {
		if existing.VersionCode == record.VersionCode {
			out = append(out, record)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].VersionCode > out[j].VersionCode
	})
	return out
}

// findHistoryRecord 在变体历史中查找指定 versionCode 的记录。
func findHistoryRecord(variant string, versionCode int) (mediaAppReleaseManifest, bool, error) {
	records, err := readMediaAppReleaseHistory(variant)
	if err != nil {
		return mediaAppReleaseManifest{}, false, err
	}
	for _, rec := range records {
		if rec.VersionCode == versionCode {
			return rec, true, nil
		}
	}
	return mediaAppReleaseManifest{}, false, nil
}

func mediaAppReleaseFilename(versionCode int, checksum string) string {
	short := checksum
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("media-center-app-v%d-%s.apk", versionCode, short)
}

// pruneOrphanAppAPKs 删除 generated/media_center 目录下不再被任何变体 manifest
// 或发布历史引用的确定性 APK 文件(media-center-app-v<N>-<hash>.apk),防止旧版本
// 无限堆积。仅删除符合该命名模式且未出现在任何 app-update*.json / app-history*.json
// 的文件;尽力而为,错误忽略(不影响本次发布)。
//
// freshlyPublished 是本次调用刚写入的 APK 文件名(并发上传时 manifest 可能
// 尚未写盘,必须显式保留,否则会被其他请求的 prune 误删);此外最近 30 秒内
// 修改的文件一律跳过,作为 rename→manifest 写盘窗口的兜底。
func pruneOrphanAppAPKs(freshlyPublished ...string) {
	pruneOrphanAppAPKsInDir(filepath.Dir(mediaCenterAppUpdateAPKPath("")), freshlyPublished)
}

// pruneOrphanAppAPKsInDir 是可注入目录的核心实现，便于用 t.TempDir() 做集成测试。
func pruneOrphanAppAPKsInDir(dir string, freshlyPublished []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	keep := make(map[string]struct{})
	for _, name := range freshlyPublished {
		if name != "" {
			keep[filepath.Base(name)] = struct{}{}
		}
	}
	// 当前发布 manifest：单对象 JSON。
	manifests, _ := filepath.Glob(filepath.Join(dir, "app-update*.json"))
	for _, m := range manifests {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var man mediaAppReleaseManifest
		if err := json.Unmarshal(data, &man); err != nil {
			continue
		}
		if name := strings.TrimSpace(man.APKFilename); name != "" {
			keep[filepath.Base(name)] = struct{}{}
		}
	}
	// 发布历史：JSON 数组，历史里每个版本都要保活。
	histories, _ := filepath.Glob(filepath.Join(dir, "app-history*.json"))
	for _, h := range histories {
		data, err := os.ReadFile(h)
		if err != nil {
			continue
		}
		var records []mediaAppReleaseManifest
		if err := json.Unmarshal(data, &records); err != nil {
			continue
		}
		for _, rec := range records {
			if name := strings.TrimSpace(rec.APKFilename); name != "" {
				keep[filepath.Base(name)] = struct{}{}
			}
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "media-center-app-v") || !strings.HasSuffix(name, ".apk") {
			continue
		}
		if _, ok := keep[name]; ok {
			continue
		}
		// 新鲜文件保护:并发上传可能在 rename 后、manifest 写盘前,
		// 此时删除会破坏刚发布的 APK。
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) < 30*time.Second {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func mediaAppReleaseAPKPath(manifest mediaAppReleaseManifest, variant string) string {
	name := filepath.Base(strings.TrimSpace(manifest.APKFilename))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return mediaCenterAppUpdateAPKPath(variant)
	}
	return filepath.Join(filepath.Dir(mediaCenterAppUpdateAPKPath(variant)), name)
}
