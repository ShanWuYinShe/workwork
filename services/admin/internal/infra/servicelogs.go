package infra

import (
	"admin/internal/platform"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GET /api/admin/services/{name}/logs?tail=100
func ApiServiceLogs(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validServiceName.MatchString(name) {
			platform.WriteError(w, "服务名不合法", 400)
			return
		}
		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "100"
		}
		if n, err := strconv.Atoi(tail); err != nil || n <= 0 || n > 10000 {
			tail = "100"
		}
		listCtx, listCancel := context.WithTimeout(r.Context(), 10*time.Second)
		containers, err := cli.ContainerList(listCtx, container.ListOptions{
			All: true,
			Filters: platform.ComposeProjectFilters(filters.KeyValuePair{
				Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", name),
			}),
		})
		listCancel()
		if err != nil {
			// daemon 故障与"服务不存在"不可区分会让排障误导;真实错误进日志。
			log.Printf("list containers for logs (%s): %v", name, err)
			platform.WriteInternalError(w, err)
			return
		}
		if len(containers) == 0 {
			platform.WriteError(w, "服务不存在", 404)
			return
		}
		logs, err := cli.ContainerLogs(r.Context(), containers[0].ID, container.LogsOptions{
			ShowStdout: true, ShowStderr: true,
			Tail: tail,
		})
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		defer logs.Close()
		// 环形缓冲:只保留最近 128KB,边读边截断。此前先全量读入再截断,
		// 一条超长日志行(巨型 stack)会让内存先翻倍再收缩。
		var buf limitWriter
		buf.max = 128 * 1024
		stdcopy.StdCopy(&buf, &buf, logs)
		s := string(buf.buf)
		platform.WriteJSON(w, map[string]any{"logs": s})
	}
}

// limitWriter 只保留最近 max 字节的写入(环形覆盖),用于日志等只需要尾部的场景。
type limitWriter struct {
	buf []byte
	max int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if len(p) >= w.max {
		w.buf = append(w.buf[:0], p[len(p)-w.max:]...)
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	return len(p), nil
}

// GET /api/admin/services/{name}/logs/stream?tail=100
func ApiServiceLogStream(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validServiceName.MatchString(name) {
			platform.WriteError(w, "服务名不合法", 400)
			return
		}
		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "100"
		}
		if n, err := strconv.Atoi(tail); err != nil || n <= 0 || n > 10000 {
			tail = "100"
		}
		listCtx, listCancel := context.WithTimeout(r.Context(), 10*time.Second)
		containers, err := cli.ContainerList(listCtx, container.ListOptions{
			All: true,
			Filters: platform.ComposeProjectFilters(filters.KeyValuePair{
				Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", name),
			}),
		})
		listCancel()
		if err != nil {
			// daemon 故障与"服务不存在"不可区分会让排障误导;真实错误进日志。
			log.Printf("list containers for logs (%s): %v", name, err)
			platform.WriteInternalError(w, err)
			return
		}
		if len(containers) == 0 {
			platform.WriteError(w, "服务不存在", 404)
			return
		}
		logs, err := cli.ContainerLogs(r.Context(), containers[0].ID, container.LogsOptions{
			ShowStdout: true, ShowStderr: true,
			Tail: tail, Follow: true,
		})
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		defer logs.Close()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			platform.WriteError(w, "当前连接不支持流式响应", 500)
			return
		}

		// Decode Docker multiplexed stream and write as SSE
		const maxFrameSize = 1 << 20 // 1MB 上限防止 OOM
		// 常驻 reader goroutine:此前每帧 go readFrame(),高流量下每秒成千
		// 次创建销毁是纯开销,且 buf 切片在闭包与主循环间靠"严格串行"隐式
		// 保证安全——任何把"消费"与"spawn 下一次"顺序调换的重构都会引入
		// 数据竞争。现在 reader 独占解码 buf,每帧拷贝出独立切片送入
		// channel(日志行典型 <1KB,拷贝开销可忽略);只有主循环写 w,
		// 退出路径上无并发写 ResponseWriter。
		type frame struct {
			data []byte // nil 表示空帧(size=0)
			err  error
		}
		ch := make(chan frame, 64)
		readerDone := make(chan struct{})
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel() // 客户端断开时通知 reader 退出
		go func() {
			defer close(readerDone)
			// send 带 ctx.Done 检查:主循环退出后发送方不会在 ch 上永久阻塞。
			send := func(f frame) bool {
				select {
				case <-ctx.Done():
					return false
				case ch <- f:
					return true
				}
			}
			var buf []byte
			hdr := make([]byte, 8)
			for {
				if _, err := io.ReadFull(logs, hdr); err != nil {
					send(frame{nil, err})
					return
				}
				// int64:32 位平台 int 为 32 位,uint32 高位置 1 会溢出为负,
				// 绕过 maxFrameSize 检查导致 buf[:size] 越界 panic。
				size := int64(binary.BigEndian.Uint32(hdr[4:8]))
				if size > maxFrameSize {
					send(frame{nil, fmt.Errorf("frame too large: %d bytes", size)})
					return
				}
				if size == 0 {
					if !send(frame{nil, nil}) {
						return
					}
					continue
				}
				if size > int64(cap(buf)) {
					buf = make([]byte, size)
				}
				if _, err := io.ReadFull(logs, buf[:size]); err != nil {
					send(frame{nil, err})
					return
				}
				// 拷贝出独立切片:buf 归 reader 所有,下一帧立即复用。
				data := make([]byte, size)
				copy(data, buf[:size])
				if !send(frame{data, nil}) {
					return
				}
			}
		}()
		defer func() {
			// 关闭 docker 流唤醒阻塞在 ReadFull 的 reader(仅靠 ctx 取消
			// 不能中断阻塞读),再等它退出,保证无 goroutine 泄漏。
			cancel()
			logs.Close()
			<-readerDone
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-ch:
				if f.err != nil {
					return
				}
				if f.data == nil {
					continue
				}
				line := strings.TrimRight(string(f.data), "\n\r")
				if line != "" {
					for _, part := range strings.Split(line, "\n") {
						// 转义 \r 防止 SSE 注入
						escaped := strings.ReplaceAll(part, "\r", "")
						fmt.Fprintf(w, "data: %s\n", escaped)
					}
					fmt.Fprint(w, "\n")
					flusher.Flush()
				}
			}
		}
	}
}
