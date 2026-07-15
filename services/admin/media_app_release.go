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
	"strconv"
	"strings"
	"time"
)

const maxMediaAppAPKSize = 300 << 20 // 300 MiB

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
			"files": map[string]string{
				"manifest": mediaCenterAppUpdateManifestPath(variant),
				"apk":      apkPath,
			},
			"apkSizeBytes": func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}(),
		})
	}
}

func apiMediaAppReleaseUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxMediaAppAPKSize+2<<20)
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			writeError(w, "上传内容过大或格式不正确", http.StatusBadRequest)
			return
		}

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
		if err := writeMediaAppReleaseManifest(manifest, variant); err != nil {
			writeInternalError(w, err)
			return
		}
		// 尽力清理不再被任何变体 manifest 引用的历史 APK,避免确定性文件名无限堆积。
		pruneOrphanAppAPKs()
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

func mediaAppReleaseFilename(versionCode int, checksum string) string {
	short := checksum
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("media-center-app-v%d-%s.apk", versionCode, short)
}

// pruneOrphanAppAPKs 删除 generated/media_center 目录下不再被任何变体 manifest
// 引用的确定性 APK 文件(media-center-app-v<N>-<hash>.apk),防止旧版本无限堆积。
// 仅删除符合该命名模式且未出现在任何 app-update*.json 的 APKFilename 中的文件;
// 尽力而为,错误忽略(不影响本次发布)。
func pruneOrphanAppAPKs() {
	dir := filepath.Dir(mediaCenterAppUpdateAPKPath(""))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	keep := make(map[string]struct{})
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
