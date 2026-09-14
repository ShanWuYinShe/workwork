package main

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
)

// GET /api/admin/services/{name}/logs?tail=100
func apiServiceLogs(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validServiceName.MatchString(name) {
			platform.WriteError(w, "Invalid service name", 400)
			return
		}
		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "100"
		}
		if n, err := strconv.Atoi(tail); err != nil || n <= 0 || n > 10000 {
			tail = "100"
		}
		containers, err := cli.ContainerList(r.Context(), container.ListOptions{
			All: true,
			Filters: platform.ComposeProjectFilters(filters.KeyValuePair{
				Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", name),
			}),
		})
		if err != nil {
			// daemon 故障与"服务不存在"不可区分会让排障误导;真实错误进日志。
			log.Printf("list containers for logs (%s): %v", name, err)
			platform.WriteInternalError(w, err)
			return
		}
		if len(containers) == 0 {
			platform.WriteError(w, "Service not found", 404)
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
func apiServiceLogStream(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validServiceName.MatchString(name) {
			platform.WriteError(w, "Invalid service name", 400)
			return
		}
		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "100"
		}
		if n, err := strconv.Atoi(tail); err != nil || n <= 0 || n > 10000 {
			tail = "100"
		}
		containers, err := cli.ContainerList(r.Context(), container.ListOptions{
			All: true,
			Filters: platform.ComposeProjectFilters(filters.KeyValuePair{
				Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", name),
			}),
		})
		if err != nil {
			// daemon 故障与"服务不存在"不可区分会让排障误导;真实错误进日志。
			log.Printf("list containers for logs (%s): %v", name, err)
			platform.WriteInternalError(w, err)
			return
		}
		if len(containers) == 0 {
			platform.WriteError(w, "Service not found", 404)
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
			platform.WriteError(w, "Streaming not supported", 500)
			return
		}

		// Decode Docker multiplexed stream and write as SSE
		const maxFrameSize = 1 << 20 // 1MB 上限防止 OOM
		buf := make([]byte, 4096)
		type frame struct {
			data []byte
			err  error
		}
		ch := make(chan frame, 1)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel() // 客户端断开时通知 readFrame 退出

		readFrame := func() {
			// 所有发送都带 ctx.Done 检查:主循环退出后(ctx 取消)发送方不会
			// 在 ch 上永久阻塞,避免每个断开连接泄漏 1-2 个 goroutine。
			send := func(f frame) {
				select {
				case <-ctx.Done():
				case ch <- f:
				}
			}
			hdr := make([]byte, 8)
			if _, err := io.ReadFull(logs, hdr); err != nil {
				send(frame{nil, err})
				return
			}
			size := int(binary.BigEndian.Uint32(hdr[4:8]))
			if size > maxFrameSize {
				send(frame{nil, fmt.Errorf("frame too large: %d bytes", size)})
				return
			}
			if size > cap(buf) {
				buf = make([]byte, size)
			} else {
				buf = buf[:size]
			}
			if size == 0 {
				send(frame{nil, nil})
				return
			}
			if _, err := io.ReadFull(logs, buf[:size]); err != nil {
				send(frame{nil, err})
				return
			}
			send(frame{buf[:size], nil})
		}

		go readFrame()
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-ch:
				if f.err != nil {
					return
				}
				if f.data == nil {
					go readFrame()
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
				go readFrame()
			}
		}
	}
}
