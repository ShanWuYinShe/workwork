package music

// 音乐曲库分片上传:大文件(>Cloudflare 单请求 100MB 上限)按 8MB 分片逐请求
// 上传,绕过边缘限制并支持断点续传。与一次性 multipart 上传(music_library.go)
// 共存:小文件批量走 multipart,大文件走分片。
//
// 协议:
//   POST /api/admin/music-library/upload-chunk  传单片(收齐自动合并落库)
//   GET  /api/admin/music-library/upload-chunk?uploadId=  查询已收分片(续传跳过)
//
// uploadId 由前端从文件名/大小/mtime/首1MB 内容派生(SHA-256 hex64),跨会话
// 稳定,刷新后重传同一文件可续传。服务端只把它当目录名,严格校验 hex64 防
// 路径穿越。每片携带该片 SHA-256,落盘前校验,保证合并后数据完整。
//
// 分片暂存于 /app/data/upload-chunks/<uploadId>/(与 music-library 平级,
// music_server 不接触);全部收齐后按序合并,原子 rename 入库并删除分片目录。
// 孤儿分片(上传中断不再继续)由每小时 ticker 清理(>24h)。

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 分片参数:8MB/片,最多 32 片(256MB),覆盖单文件 200MB 上限。
const (
	musicChunkBytes     = 8 << 20
	musicMaxChunks      = 32
	musicChunkBodyLimit = musicChunkBytes + 2<<20 // 单片 + multipart 开销
	musicChunkTTL       = 24 * time.Hour
)

// uploadChunksDir 与 musicLibraryDir 同卷(/app/data),分片暂存根目录。
var uploadChunksDir = "/app/data/upload-chunks"

// chunkMeta 是分片会话元信息,首片到达时写入。
type chunkMeta struct {
	FileName string `json:"fileName"`
	Total    int    `json:"total"`
}

// chunkUploadError 业务校验错误,消息可直接返回客户端。
type chunkUploadError struct {
	msg  string
	code int
}

func (e *chunkUploadError) Error() string { return e.msg }

func chunkErr(msg string, code int) error {
	return &chunkUploadError{msg: msg, code: code}
}

// validUploadID 严格 hex64,杜绝把用户可控字符串拼进路径。
func validUploadID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func chunkDir(uploadID string) string { return filepath.Join(uploadChunksDir, uploadID) }

func readChunkMeta(uploadID string) (*chunkMeta, error) {
	data, err := os.ReadFile(filepath.Join(chunkDir(uploadID), "meta.json"))
	if err != nil {
		return nil, err
	}
	var m chunkMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// receivedChunks 列出已落盘分片序号(忽略 .part 之外的杂项)。
func receivedChunks(uploadID string) ([]int, error) {
	entries, err := os.ReadDir(chunkDir(uploadID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []int{}
	for _, e := range entries {
		idx, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".part"))
		if err != nil || e.IsDir() {
			continue
		}
		out = append(out, idx)
	}
	sort.Ints(out)
	return out, nil
}

// ApiMusicChunkQuery 续传查询:返回已收分片与元信息;无会话时 received 为空。
func ApiMusicChunkQuery() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uploadID := r.URL.Query().Get("uploadId")
		if !validUploadID(uploadID) {
			platform.WriteError(w, "uploadId 不合法", http.StatusBadRequest)
			return
		}
		resp := map[string]any{"received": []int{}, "total": 0, "fileName": ""}
		if meta, err := readChunkMeta(uploadID); err == nil {
			rc, err := receivedChunks(uploadID)
			if err != nil {
				platform.WriteInternalError(w, err)
				return
			}
			resp["received"] = rc
			resp["total"] = meta.Total
			resp["fileName"] = meta.FileName
		}
		platform.WriteJSON(w, resp)
	}
}

// ApiMusicChunkUpload 接收单片;全部收齐时自动按序合并入库。
func ApiMusicChunkUpload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 并发闸门(4MB multipart 内存缓冲,与全部上传端点共享 2 位额度)。
		release, ok := platform.AcquireUploadSlot(r.Context())
		if !ok {
			platform.WriteError(w, "too many concurrent uploads", http.StatusTooManyRequests)
			return
		}
		defer release()

		r.Body = http.MaxBytesReader(w, r.Body, musicChunkBodyLimit)
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			platform.WriteError(w, "分片内容过大或格式不正确", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()

		uploadID := r.FormValue("uploadId")
		fileName := r.FormValue("fileName")
		total, _ := strconv.Atoi(r.FormValue("total"))
		index, _ := strconv.Atoi(r.FormValue("index"))
		chunkSHA := r.FormValue("sha256")
		if !validUploadID(uploadID) {
			platform.WriteError(w, "uploadId 不合法", http.StatusBadRequest)
			return
		}
		if total < 1 || total > musicMaxChunks || index < 0 || index >= total {
			platform.WriteError(w, "分片序号不合法", http.StatusBadRequest)
			return
		}
		// 文件名规则与一次性上传一致(拒绝路径分隔符 + 扩展名白名单)。
		if fileName == "" || strings.ContainsAny(fileName, `/\`) ||
			!musicLibraryAllowedExt[strings.ToLower(filepath.Ext(fileName))] {
			platform.WriteError(w, "文件名不合法(仅支持音频文件)", http.StatusBadRequest)
			return
		}
		fileHeader := r.MultipartForm.File["chunk"]
		if len(fileHeader) != 1 {
			platform.WriteError(w, "缺少分片数据", http.StatusBadRequest)
			return
		}
		if fileHeader[0].Size > musicChunkBytes {
			platform.WriteError(w, "分片超过 8MB 上限", http.StatusBadRequest)
			return
		}
		if _, err := hex.DecodeString(chunkSHA); err != nil {
			platform.WriteError(w, "分片校验值不合法", http.StatusBadRequest)
			return
		}

		if err := os.MkdirAll(chunkDir(uploadID), 0o755); err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		// 首片隐式重建会话:同名会话但布局变了(文件被修改后重传)则丢弃旧分片。
		if meta, err := readChunkMeta(uploadID); err == nil {
			if meta.Total != total || meta.FileName != fileName {
				if err := os.RemoveAll(chunkDir(uploadID)); err != nil {
					platform.WriteInternalError(w, err)
					return
				}
				if err := os.MkdirAll(chunkDir(uploadID), 0o755); err != nil {
					platform.WriteInternalError(w, err)
					return
				}
			}
		} else if !os.IsNotExist(err) {
			platform.WriteInternalError(w, err)
			return
		}
		if err := platform.AtomicWriteFile(filepath.Join(chunkDir(uploadID), "meta.json"),
			mustJSON(chunkMeta{FileName: fileName, Total: total}), 0o644); err != nil {
			platform.WriteInternalError(w, err)
			return
		}

		saved, err := saveChunk(uploadID, index, chunkSHA, fileHeader[0])
		if err != nil {
			respondChunkErr(w, err)
			return
		}
		rc, err := receivedChunks(uploadID)
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		resp := map[string]any{"received": rc, "total": total, "complete": false}
		if len(rc) == total {
			// 收齐:合并入库(同名覆盖语义与一次性上传一致)。
			finalName, err := mergeChunkOnce(uploadID, total)
			if err != nil {
				respondChunkErr(w, err)
				return
			}
			// 分片合并与一次性上传是同级的变更类动作(写曲库),按 AGENTS 审计
			// 约定留痕;文件名 %q 转义防日志注入(同 music library delete)。
			_, actor := auth.GetUserFromContext(r)
			log.Printf("[security] music chunk upload complete file=%q chunks=%d by=%s", finalName, total, actor)
			resp["complete"] = true
			resp["saved"] = finalName
		}
		if saved {
			log.Printf("music chunk upload: id=%s index=%d/%d", uploadID, index+1, total)
		}
		platform.WriteJSON(w, resp)
	}
}

// respondChunkErr 业务错误返回客户端,内部错误统一 500。
func respondChunkErr(w http.ResponseWriter, err error) {
	var ce *chunkUploadError
	if errors.As(err, &ce) {
		platform.WriteError(w, ce.msg, ce.code)
		return
	}
	platform.WriteInternalError(w, err)
}

// saveChunk 校验单片 SHA-256 后原子落盘;重复片幂等(返回 saved=false),但
// 已存在分片与新片 SHA 不一致时报 409——静默接受旧分片会让断点续传把损坏/
// 不同来源的内容合并入库,且上传者无从得知结果与意图不符。
func saveChunk(uploadID string, index int, chunkSHA string, header *multipart.FileHeader) (bool, error) {
	partPath := filepath.Join(chunkDir(uploadID), fmt.Sprintf("%06d.part", index))
	if existing, err := os.ReadFile(partPath); err == nil {
		sum := sha256.Sum256(existing)
		if hex.EncodeToString(sum[:]) == chunkSHA {
			return false, nil // 内容一致:续传幂等
		}
		return false, chunkErr("同序号分片已存在且内容不一致,请更换 uploadId 重传", http.StatusConflict)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	src, err := header.Open()
	if err != nil {
		return false, err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(chunkDir(uploadID), ".chunk-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // 正常路径已 rename,失败路径清理
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), src); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != chunkSHA {
		return false, chunkErr("分片校验失败,请重传该分片", http.StatusConflict)
	}
	if err := os.Rename(tmpName, partPath); err != nil {
		return false, err
	}
	return true, nil
}

// chunk 合并的一次性守卫：重传最后一片（如超时重试）会让多个请求同时看到
// "收齐"并各自执行 mergeChunks，先完成者清理分片目录后，其余请求对已删除
// 目录拷贝报错——文件实际已入库却向客户端返回失败。serialize + 结果缓存保证
// 同一 uploadId 只合并一次。条目由延迟删除兜底回收（uploadId 会话级唯一，
// 条目极小，TTL 只需覆盖并发重传窗口）。
var (
	chunkMergeMu     sync.Mutex
	chunkMergedNames = map[string]string{}
)

func mergeChunkOnce(uploadID string, total int) (string, error) {
	chunkMergeMu.Lock()
	defer chunkMergeMu.Unlock()
	if name, ok := chunkMergedNames[uploadID]; ok {
		return name, nil
	}
	name, err := mergeChunks(uploadID, total)
	if err == nil {
		chunkMergedNames[uploadID] = name
		time.AfterFunc(10*time.Minute, func() {
			chunkMergeMu.Lock()
			delete(chunkMergedNames, uploadID)
			chunkMergeMu.Unlock()
		})
	}
	return name, err
}

// mergeChunks 按序拼接全部分片,校验总大小后原子入库,清理分片目录。
func mergeChunks(uploadID string, total int) (string, error) {
	meta, err := readChunkMeta(uploadID)
	if err != nil {
		return "", err
	}
	rc, err := receivedChunks(uploadID)
	if err != nil {
		return "", err
	}
	if len(rc) != total {
		return "", chunkErr("分片不完整", http.StatusConflict)
	}
	if err := os.MkdirAll(musicLibraryDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(musicLibraryDir, ".music-merge-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	var totalBytes int64
	for _, idx := range rc {
		f, err := os.Open(filepath.Join(chunkDir(uploadID), fmt.Sprintf("%06d.part", idx)))
		if err != nil {
			tmp.Close()
			return "", err
		}
		n, err := io.Copy(tmp, f)
		f.Close()
		if err != nil {
			tmp.Close()
			return "", err
		}
		totalBytes += n
	}
	// rename 前 fsync,与 saveMusicUpload/saveChunk 的数据保护对称;Sync 必须在 Close 之前。
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if totalBytes > maxMusicFileBytes {
		return "", chunkErr("文件超过 200MB 上限", http.StatusBadRequest)
	}
	if err := os.Rename(tmpName, filepath.Join(musicLibraryDir, meta.FileName)); err != nil {
		return "", err
	}
	if err := os.RemoveAll(chunkDir(uploadID)); err != nil {
		log.Printf("music chunk merge: cleanup %s failed: %v", uploadID, err)
	}
	log.Printf("music chunk merged: id=%s name=%q bytes=%d", uploadID, meta.FileName, totalBytes)
	return meta.FileName, nil
}

// mustJSON 序列化失败直接 panic:meta 结构只含基础类型,不可能失败。
func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// CleanupExpiredChunkUploads 清理超过 TTL 的孤儿分片会话(上传中断不再继续)。
func CleanupExpiredChunkUploads() {
	entries, err := os.ReadDir(uploadChunksDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("music chunk cleanup: %v", err)
		}
		return
	}
	cutoff := time.Now().Add(-musicChunkTTL)
	for _, e := range entries {
		if !e.IsDir() || !validUploadID(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(uploadChunksDir, e.Name())); err != nil {
			log.Printf("music chunk cleanup: %s: %v", e.Name(), err)
		} else {
			log.Printf("music chunk cleanup: removed expired %s", e.Name())
		}
	}
}

// StartChunkCleanupLoop 每小时执行一次过期清理,随进程退出终止(同 transferCleanupLoop)。
func StartChunkCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			CleanupExpiredChunkUploads()
		case <-ctx.Done():
			return
		}
	}
}
