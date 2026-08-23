package main

// 音乐曲库管理:管理面板「音乐曲库」页的后端,读写 music_server 的曲库
// 目录(compose 把宿主机 .wework/music-library 同时挂给 admin(rw,本模块)
// 与 music_server(ro,扫描播放))。上传即入库:music_server 索引 60s TTL
// 懒刷新,无需重启。
//
// 安全约束:文件名只保留 basename(拒绝任何路径分隔符,防穿越);扩展名
// 白名单与 music_server supportedExt 一致;单文件与请求体双重大小上限;
// 写入走临时文件 + rename 原子落盘,同名覆盖(重传修正版语义)。

import (
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// musicLibraryDir 是容器内曲库目录。compose 已把宿主机 .wework 整体挂为
// /app/data(admin 与 music_server 共享同一目录,admin rw / music_server ro),
// 曲库即其中的 music-library/ 子目录,无需额外挂载。var 供测试替换。
var musicLibraryDir = "/app/data/music-library"

// 单文件与整个 multipart 请求的大小上限。
const (
	maxMusicFileBytes = 200 << 20
	maxMusicBodyBytes = maxMusicFileBytes*2 + 8<<20
)

// musicLibraryAllowedExt 与 music_server 的 supportedExt 保持一致。
var musicLibraryAllowedExt = map[string]bool{
	".mp3": true, ".wav": true, ".flac": true, ".m4a": true,
	".ogg": true, ".oga": true, ".aac": true, ".opus": true,
}

var (
	errMusicInvalidName = errors.New("文件名不合法")
	errMusicInvalidExt  = errors.New("仅支持音频文件(mp3/wav/flac/m4a/ogg/aac/opus)")
	errMusicTooLarge    = errors.New("文件超过 200MB 上限")
)

// musicLibraryFile 是列表条目。
type musicLibraryFile struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	ModTime   string `json:"modTime"`
}

func apiMusicLibraryList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(musicLibraryDir)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, map[string]any{"files": []musicLibraryFile{}})
				return
			}
			writeInternalError(w, err)
			return
		}
		files := make([]musicLibraryFile, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || !musicLibraryAllowedExt[strings.ToLower(filepath.Ext(e.Name()))] {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, musicLibraryFile{
				Name:      e.Name(),
				SizeBytes: info.Size(),
				ModTime:   info.ModTime().Format(time.RFC3339),
			})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		writeJSON(w, map[string]any{"files": files})
	}
}

func apiMusicLibraryUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxMusicBodyBytes)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeError(w, "上传内容过大或格式不正确", http.StatusBadRequest)
			return
		}
		// 超过内存阈值的 multipart 部分落盘为临时文件,请求结束必须显式清理。
		defer r.MultipartForm.RemoveAll()

		if err := os.MkdirAll(musicLibraryDir, 0o755); err != nil {
			writeInternalError(w, err)
			return
		}
		headers := r.MultipartForm.File["files"]
		if len(headers) == 0 {
			writeError(w, "请选择要上传的音频文件", http.StatusBadRequest)
			return
		}
		if len(headers) > 50 {
			writeError(w, "单次最多上传 50 个文件", http.StatusBadRequest)
			return
		}

		saved := make([]string, 0, len(headers))
		skipped := make([]string, 0)
		for _, header := range headers {
			name, err := saveMusicUpload(header)
			if err != nil {
				skipped = append(skipped, header.Filename+"("+err.Error()+")")
				continue
			}
			saved = append(saved, name)
		}
		log.Printf("music library upload: %d saved, %d skipped", len(saved), len(skipped))
		writeJSON(w, map[string]any{"saved": saved, "skipped": skipped})
	}
}

// saveMusicUpload 校验并保存单个上传文件,返回落盘文件名。
func saveMusicUpload(header *multipart.FileHeader) (string, error) {
	// 原始名带路径分隔符一律拒绝(浏览器 multipart 不会发送路径名,
	// 出现即视为异常输入),不做 basename 洗白以免产生语义不明的落盘名。
	raw := strings.TrimSpace(header.Filename)
	if raw == "" || strings.ContainsAny(raw, `/\`) {
		return "", errMusicInvalidName
	}
	name := filepath.Base(raw)
	if name == "." || name == ".." {
		return "", errMusicInvalidName
	}
	if !musicLibraryAllowedExt[strings.ToLower(filepath.Ext(name))] {
		return "", errMusicInvalidExt
	}
	if header.Size > maxMusicFileBytes {
		return "", errMusicTooLarge
	}
	src, err := header.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	// 同名文件覆盖语义:投放端可能重传修正过的音频,原子替换。
	tmp, err := os.CreateTemp(musicLibraryDir, ".music-upload-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // 正常路径已 rename,失败路径清理
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, filepath.Join(musicLibraryDir, name)); err != nil {
		return "", err
	}
	return name, nil
}

func apiMusicLibraryDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(strings.TrimSpace(r.URL.Query().Get("name")))
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			writeError(w, "文件名不合法", http.StatusBadRequest)
			return
		}
		if !musicLibraryAllowedExt[strings.ToLower(filepath.Ext(name))] {
			writeError(w, "文件名不合法", http.StatusBadRequest)
			return
		}
		if err := os.Remove(filepath.Join(musicLibraryDir, name)); err != nil {
			if os.IsNotExist(err) {
				writeError(w, "文件不存在", http.StatusNotFound)
				return
			}
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]string{"deleted": name})
	}
}
