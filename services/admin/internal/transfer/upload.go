package transfer

import (
	"admin/internal/platform"
	"admin/internal/requestctx"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func ApiTransferUpload(s *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := requestctx.ActorFrom(r.Context())

		// 并发闸门(32MB multipart 内存缓冲,全局限 2 位,防并发上传 OOM)。
		release, ok := platform.AcquireUploadSlot(r.Context())
		if !ok {
			platform.WriteError(w, "并发上传过多，请稍后重试", http.StatusTooManyRequests)
			return
		}
		defer release()

		// 请求体硬上限（单文件 2GB + multipart 开销）。ParseMultipartForm 会把
		// 全部 file part 先无上限 spool 到磁盘才返回，header.Size 预检与配额
		// 检查都发生在其后——没有这层，超大请求体先把磁盘写满。nginx 侧的
		// client_max_body_size 只保护经 nginx 的路径，直连 admin 的内部调用
		// 不经过它。
		r.Body = http.MaxBytesReader(w, r.Body, transferMaxFile+64<<20)

		// 限制 multipart 内存缓冲为 32MB；更大的部分自动落盘为临时文件，
		// 避免单次上传占用 2GB 堆内存导致 OOM。单文件上限在下方强制执行。
		if err := r.ParseMultipartForm(transferUploadMemory); err != nil {
			// 请求体超出 MaxBytesReader 上限是容量问题,语义上应为 413;
			// 其余(格式错误)保持 400。http.MaxBytesError 自 Go 1.19 起可
			// 从错误链中识别。
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				platform.WriteError(w, fmt.Sprintf("文件过大（最大 %dGB）", transferMaxFile>>30), http.StatusRequestEntityTooLarge)
				return
			}
			platform.WriteError(w, "请求格式错误", 400)
			return
		}
		// multipart 落盘的临时文件不会被自动清理，请求结束时显式删除。
		defer func() {
			if r.MultipartForm != nil {
				r.MultipartForm.RemoveAll()
			}
		}()

		file, header, err := r.FormFile("file")
		if err != nil {
			platform.WriteError(w, "未找到上传文件", 400)
			return
		}
		defer file.Close()

		// 校验文件名
		name := strings.TrimSpace(filepath.Base(header.Filename))
		if !validTransferFilename(name) {
			platform.WriteError(w, "文件名不合法（不允许路径分隔符或控制字符，最长 255 字符）", 400)
			return
		}

		// 单文件大小预检（header.Size 可靠时直接拒绝超大文件）
		if header.Size > transferMaxFile {
			platform.WriteError(w, fmt.Sprintf("文件超过 %dGB 上限", transferMaxFile>>30), http.StatusRequestEntityTooLarge)
			return
		}

		// 解析有效期:非法值显式拒绝,不再静默回退默认——静默回退会执行与
		// 上传者意图不符的生命周期(以为传 300 天实际 7 天就消失)。
		expiresIn := r.FormValue("expires_in")
		duration := transferDefaultExp
		if expiresIn != "" {
			d, err := time.ParseDuration(expiresIn)
			if err != nil || d <= 0 || d > 365*24*time.Hour {
				platform.WriteError(w, "有效期不合法（须为正的时长，最长 365 天，如 720h）", 400)
				return
			}
			duration = d
		}

		// 解析最大下载次数:同样显式拒绝非法值。
		maxDL := 0
		if v := r.FormValue("max_downloads"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				platform.WriteError(w, "下载上限不合法（须为非负整数，0 表示不限）", 400)
				return
			}
			maxDL = n
		}

		// Phase 0: 配额预检——在把整个文件写入磁盘之前,按 multipart 已知的
		// header.Size 快照检查全局/per-user 配额。没有这层,并发发起 N 个大上传
		// 可以先把磁盘塞满再在 Phase 2 逐一失败,配额形同虚设。Phase 2 仍保留
		// 基于实际写入量的精确检查(预检只是快速失败,不追求精确)。
		s.mu.Lock()
		snapshot, err := s.loadLocked()
		if err != nil {
			s.mu.Unlock()
			log.Printf("ERROR: transfer upload precheck: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		if msg := checkUploadQuota(snapshot, actor.Username, header.Size); msg != "" {
			s.mu.Unlock()
			platform.WriteError(w, msg, 400)
			return
		}
		s.mu.Unlock()

		// Phase 1: 文件写入磁盘（在锁外进行，避免阻塞其他 transfer 操作）
		if err := os.MkdirAll(s.dir(), 0755); err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		// os.CreateTemp 用随机后缀,极端并发下不会像 UnixNano 那样 O_EXCL 碰撞。
		tmpFile, err := os.CreateTemp(s.dir(), ".upload-*.tmp")
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		tmpPath := tmpFile.Name()
		dst := tmpFile

		written, err := io.Copy(dst, io.LimitReader(file, transferMaxFile))
		// rename 前 fsync:断电不会留下"manifest 记了 2GB、文件内容为空"的
		// 坏文件(下载端无校验和可发现)。
		if serr := dst.Sync(); serr != nil && err == nil {
			err = serr
		}
		dst.Close()
		if err != nil {
			os.Remove(tmpPath)
			platform.WriteInternalError(w, err)
			return
		}

		// 截断检测：写入量达到上限时，尝试再读 1 字节判断是否还有数据
		if written >= transferMaxFile {
			var probe [1]byte
			n, _ := file.Read(probe[:])
			if n > 0 {
				os.Remove(tmpPath)
				platform.WriteError(w, fmt.Sprintf("文件超过 %dGB 上限", transferMaxFile>>30), http.StatusRequestEntityTooLarge)
				return
			}
		}

		// Phase 2: 加锁 — 用实际写入量做容量检查 + 生成分享码 + 重命名 + 更新 manifest
		s.mu.Lock()
		m, err := s.loadLocked()
		if err != nil {
			s.mu.Unlock()
			os.Remove(tmpPath)
			log.Printf("ERROR: transfer upload: load manifest: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		if msg := checkUploadQuota(m, actor.Username, written); msg != "" {
			s.mu.Unlock()
			os.Remove(tmpPath)
			platform.WriteError(w, msg, 400)
			return
		}

		code, err := generateUniqueTransferCode(m)
		if err != nil {
			s.mu.Unlock()
			os.Remove(tmpPath)
			platform.WriteInternalError(w, err)
			return
		}

		dstPath := s.filePath(code, name)
		if err := os.Rename(tmpPath, dstPath); err != nil {
			s.mu.Unlock()
			os.Remove(tmpPath)
			platform.WriteInternalError(w, err)
			return
		}

		// 更新 manifest
		now := time.Now().UTC()
		entry := transferFile{
			Code:         code,
			Filename:     name,
			Size:         written,
			ContentType:  header.Header.Get("Content-Type"),
			Uploader:     actor.Username,
			UploadedAt:   now.Format(time.RFC3339),
			ExpiresAt:    now.Add(duration).Format(time.RFC3339),
			MaxDownloads: maxDL,
			Downloads:    0,
		}
		m.Files = append(m.Files, entry)
		m.TotalSize += written
		saveErr := s.saveLocked(m)
		s.mu.Unlock()

		if saveErr != nil {
			log.Printf("ERROR: save transfer manifest (upload): %v", saveErr)
			// 磁盘文件已就位但 manifest 未持久化 → 孤儿文件:不在 manifest 里,
			// 列表看不到、删除接口删不到、且不占配额计数——没有任何回收路径,
			// 配额检查会持续放行,磁盘满时滚雪球。尽力回滚删除;删除也失败
			// (如目录只读)时带路径记 ERROR,交运维手工清理。
			if rmErr := os.Remove(dstPath); rmErr != nil {
				log.Printf("ERROR: transfer orphan cleanup failed, manual removal needed: %s: %v", dstPath, rmErr)
			}
			platform.WriteInternalError(w, fmt.Errorf("persist transfer manifest: %w", saveErr))
			return
		}

		log.Printf("[security] transfer upload code=%q file=%q size=%d by=%s", code, entry.Filename, entry.Size, actor.Username)
		platform.WriteJSON(w, map[string]any{
			"status": "ok",
			"code":   code,
			"file":   entry,
		})
	}
}
