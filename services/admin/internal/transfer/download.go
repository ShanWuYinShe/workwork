package transfer

import (
	"admin/internal/platform"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func ApiTransferPublicDownload(s *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if code == "" || len(code) != transferCodeLen {
			platform.WriteError(w, "无效的分享码", 400)
			return
		}
		// 只允许 alphanumeric
		for _, c := range code {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				platform.WriteError(w, "无效的分享码", 400)
				return
			}
		}

		s.mu.Lock()
		m, err := s.loadLocked()
		if err != nil {
			s.mu.Unlock()
			log.Printf("ERROR: transfer download: load manifest: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		var target *transferFile
		for i := range m.Files {
			if m.Files[i].Code == code {
				target = &m.Files[i]
				break
			}
		}

		if target == nil {
			s.mu.Unlock()
			platform.WriteError(w, "文件不存在或已过期", 404)
			return
		}

		// 检查过期
		if target.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, target.ExpiresAt); err == nil && time.Now().UTC().After(t) {
				s.mu.Unlock()
				platform.WriteError(w, "分享链接已过期", 410)
				return
			}
		}

		// 检查下载次数
		if target.MaxDownloads > 0 && target.Downloads >= target.MaxDownloads {
			s.mu.Unlock()
			platform.WriteError(w, "下载次数已达上限", 410)
			return
		}

		// 预留计数：检查与自增必须在同一把锁内完成。此前检查在锁内、自增
		// 在 ServeContent 之后，并发请求都能通过检查，限次链接被放大成
		// 任意次。先预留，HEAD/416/未覆盖末字节的 range/打开失败在响应后回退。
		target.Downloads++
		if err := s.saveLocked(m); err != nil {
			if target.MaxDownloads > 0 {
				s.mu.Unlock()
				log.Printf("ERROR: transfer download: persist reserved count: %v", err)
				platform.WriteInternalError(w, err)
				return
			}
			// 不限次链接：计数只用于展示，持久化失败不阻断下载。
			log.Printf("WARNING: transfer download: persist count (unlimited): %v", err)
		}

		// specCoversLast 只表达请求意图，是否兑现由 countingResponseWriter
		// 的 status/written 确认（见 deliveredFullDownload）。
		specCoversLast := rangeCountsAsDownload(r.Header.Get("Range"), target.Size)
		filePath := s.filePath(code, target.Filename)
		// mime.FormatMediaType 生成 RFC 5987 编码(非 ASCII 文件名输出
		// filename*=utf-8''...,ASCII 输出普通 filename="..."),各浏览器
		// 都能以原文件名保存;非法输入返回空串时退化为纯 attachment。
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": target.Filename})
		if disposition == "" {
			disposition = "attachment"
		}

		// 在锁内打开文件获得 FD:ServeContent 基于已打开的 FD 提供内容,
		// 并发删除只移除目录项,FD 仍可完整读取——消除 unlock→ServeFile
		// 之间文件被删导致 404 的 TOCTOU 窗口。
		f, err := os.Open(filePath)
		if err != nil {
			s.mu.Unlock()
			refundTransferDownload(s, code)
			if os.IsNotExist(err) {
				platform.WriteError(w, "文件不存在或已过期", 404)
				return
			}
			log.Printf("transfer download: open %s: %v", code, err)
			platform.WriteInternalError(w, err)
			return
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			s.mu.Unlock()
			refundTransferDownload(s, code)
			log.Printf("transfer download: stat %s: %v", code, err)
			platform.WriteInternalError(w, err)
			return
		}
		s.mu.Unlock()
		defer f.Close()

		w.Header().Set("Content-Disposition", disposition)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("Accept-Ranges", "bytes")
		cw := &countingResponseWriter{ResponseWriter: w}
		http.ServeContent(cw, r, target.Filename, info.ModTime(), f)

		// 按实际交付结果决定是否保留预留计数：HEAD(无响应体)、416、未覆盖
		// 末字节的 range 不算一次下载，回退；完整/覆盖末字节的交付保留。
		if !deliveredFullDownload(r, cw, specCoversLast, info.Size()) {
			refundTransferDownload(s, code)
		}
	}
}

// refundTransferDownload 回退一次预留的下载计数。读取失败只记日志：计数
// 回退失败不应该影响已经完成的下载响应。
func refundTransferDownload(s *Store, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadLocked()
	if err != nil {
		log.Printf("ERROR: transfer download refund: load manifest: %v", err)
		return
	}
	for i := range m.Files {
		if m.Files[i].Code != code {
			continue
		}
		if m.Files[i].Downloads > 0 {
			m.Files[i].Downloads--
			if err := s.saveLocked(m); err != nil {
				log.Printf("ERROR: transfer download refund: save: %v", err)
			}
		}
		return
	}
}

// countingResponseWriter 记录 ServeContent 实际写出的状态码与字节数,供下载
// 计数判断"是否真的交付了内容"。
type countingResponseWriter struct {
	http.ResponseWriter
	status   int
	written  int64
	writeErr bool
}

func (c *countingResponseWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *countingResponseWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	n, err := c.ResponseWriter.Write(p)
	c.written += int64(n)
	if err != nil {
		c.writeErr = true
	}
	return n, err
}

// Flush 透传: ServeContent 在无缓冲场景可能使用 Flusher。
func (c *countingResponseWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// deliveredFullDownload 判定一次请求是否真正消耗了下载次数:排除 HEAD(永无
// 响应体)、416 等错误响应(没有内容发出)、客户端中途断开(writeErr)与不
// 覆盖末字节的中间分段(specCoversLast=false)。200 须发满全文件;206 须请求
// 规格覆盖末字节(下载管理器的收尾分段恰好计 1,完整下载仍恰好计 1)。
func deliveredFullDownload(r *http.Request, cw *countingResponseWriter, specCoversLast bool, size int64) bool {
	if r.Method == http.MethodHead {
		return false
	}
	if cw.writeErr || (cw.status != http.StatusOK && cw.status != http.StatusPartialContent) {
		return false
	}
	if !specCoversLast {
		return false
	}
	if cw.status == http.StatusOK {
		return cw.written >= size
	}
	return cw.written > 0
}

// rangeCountsAsDownload 判断一次（可能带 Range 的）请求"按意图"是否取到
// 文件末字节——它是下载计数的必要条件之一,充分条件由
// deliveredFullDownload 按实际响应(200/206、written、writeErr)确认。
//
// 任何完整下载——单连接无 Range、或多连接下载管理器的某个分段——都必然取到
// 文件的最后一个字节。因此只有"覆盖文件末字节"的规格才计：完整下载恰好计 1
// （取末字节的那个分段），中间续传分段（bytes=1048576-2097151）不计。后缀范围
// bytes=-N（N≥文件大小即取到全文末字节）与 bytes=1-（跳过首字节的开放范围，
// 覆盖末字节）必须计数。解析不了的规格保守按"覆盖"处理——实际是否发出内容由
// ServeContent 的响应状态决定(非法/越界范围得到 416,不会计数)。
func rangeCountsAsDownload(rangeHeader string, size int64) bool {
	if rangeHeader == "" {
		return true
	}
	trimmed := strings.TrimSpace(rangeHeader)
	lower := strings.ToLower(trimmed)
	spec := strings.TrimPrefix(lower, "bytes=")
	if spec == lower {
		return true // 缺 bytes= 前缀的非法头：保守计数
	}
	if strings.ContainsRune(spec, ',') {
		return true // 多段范围：保守计数
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return true // 非法格式：保守计数
	}
	start, end := strings.TrimSpace(spec[:dash]), strings.TrimSpace(spec[dash+1:])
	if start == "" {
		return true // 后缀范围 bytes=-N：覆盖末字节
	}
	s, err := strconv.ParseInt(start, 10, 64)
	if err != nil || s < 0 || s >= size {
		return true // 非法/越界起始：保守计数
	}
	if end == "" {
		return true // 开放范围 bytes=S-：覆盖末字节
	}
	e, err := strconv.ParseInt(end, 10, 64)
	if err != nil {
		return true // 非法结束值：保守计数
	}
	return e >= size-1 // 封闭分段：仅当覆盖末字节时计数
}
