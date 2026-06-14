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
	return func(w http.ResponseWriter, _ *http.Request) {
		manifest, exists, err := readMediaAppReleaseManifest()
		if err != nil {
			writeInternalError(w, err)
			return
		}
		apkPath := mediaAppReleaseAPKPath(manifest)
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
				"manifest": mediaCenterAppUpdateManifestPath,
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

		if err := os.MkdirAll(filepath.Dir(mediaCenterAppUpdateAPKPath), 0755); err != nil {
			writeInternalError(w, err)
			return
		}
		tmp, err := os.CreateTemp(filepath.Dir(mediaCenterAppUpdateAPKPath), ".media-center-app.apk.tmp-*")
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
		apkPath := filepath.Join(filepath.Dir(mediaCenterAppUpdateAPKPath), apkFilename)
		if err := os.Rename(tmpPath, apkPath); err != nil {
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
		if err := writeMediaAppReleaseManifest(manifest); err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"manifest": manifest,
		})
	}
}

func readMediaAppReleaseManifest() (mediaAppReleaseManifest, bool, error) {
	data, err := os.ReadFile(mediaCenterAppUpdateManifestPath)
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

func writeMediaAppReleaseManifest(manifest mediaAppReleaseManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(mediaCenterAppUpdateManifestPath, data, 0644)
}

func mediaAppReleaseFilename(versionCode int, checksum string) string {
	short := checksum
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("media-center-app-v%d-%s.apk", versionCode, short)
}

func mediaAppReleaseAPKPath(manifest mediaAppReleaseManifest) string {
	name := filepath.Base(strings.TrimSpace(manifest.APKFilename))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return mediaCenterAppUpdateAPKPath
	}
	return filepath.Join(filepath.Dir(mediaCenterAppUpdateAPKPath), name)
}
