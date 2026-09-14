package proxy

import (
	"admin/internal/platform"
	"bytes"
	"context"
	"io"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

var xhttpPostRE = regexp.MustCompile(`"POST /proxy/query/\S+ HTTP/\S+" (\d{3})`)

const (
	checkInterval  = 5 * time.Minute
	checkWindow    = "2m"
	cooldownPeriod = 15 * time.Minute
	// minBadPosts 是触发重启的最低失败数（绝对阈值）：窗口内零星 400
	// （单个客户端协议不匹配）不会触发。
	minBadPosts = 3
	// minBadRatioPct 是失败占比阈值（百分比）：窗口内混入少量正常请求时
	// （如合法客户端正在使用）不判定为 transport 整体损坏。旧逻辑
	// `total == bad` 在攻击者用认证后的垃圾 POST 制造全 400 时会把 xray
	// 拖入重启循环（每冷却期重启一次 → 全体代理周期性抖动）。
	minBadRatioPct = 80
	// maxRestartsPerHour 是 1 小时内的重启次数上限：达到上限后只告警不
	// 重启，防止自愈逻辑本身成为 DoS 向量。
	maxRestartsPerHour = 2
)

func XhttpHealthChecker(ctx context.Context, cli *client.Client) {
	// restartHistory 保存最近重启时间，用于冷却期 + 每小时上限双重约束。
	var restartHistory []time.Time
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if len(restartHistory) > 0 && time.Since(restartHistory[len(restartHistory)-1]) < cooldownPeriod {
				continue
			}
			total, bad := countXHTTPPosts(ctx, cli)
			// 双条件：失败数达标 且 失败占比达标（bad*100 >= total*80，
			// total 为 0 时不成立，天然避免除零）。
			if bad >= minBadPosts && bad*100 >= total*minBadRatioPct {
				// 滑动 1 小时窗口计数，裁剪过期记录。
				cutoff := time.Now().Add(-time.Hour)
				recent := 0
				kept := restartHistory[:0]
				for _, t := range restartHistory {
					if t.After(cutoff) {
						kept = append(kept, t)
						recent++
					}
				}
				restartHistory = kept
				if recent >= maxRestartsPerHour {
					log.Printf("[xhttp-health] %d/%d POSTs returned 400, but restart limit %d/h reached; skipping", bad, total, maxRestartsPerHour)
					continue
				}
				log.Printf("[xhttp-health] xhttp transport broken: %d/%d POSTs returned 400, restarting xray", bad, total)
				// 显式 deadline:后台 goroutine 继承的是 Background ctx,
				// docker.sock 挂起时无此超时会永久卡住整个自检循环
				// (与 apiServiceAction 的 2min 对齐)。
				restartCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				err := RestartXray(restartCtx, cli)
				cancel()
				if err != nil {
					log.Printf("[xhttp-health] restart failed: %v", err)
					continue
				}
				restartHistory = append(restartHistory, time.Now())
				log.Printf("[xhttp-health] xray restarted successfully")
			}
		}
	}
}

func countXHTTPPosts(ctx context.Context, cli *client.Client) (total, bad int) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	containers, err := cli.ContainerList(ctx, container.ListOptions{
		Filters: platform.ComposeProjectFilters(filters.KeyValuePair{
			Key: "label", Value: "com.docker.compose.service=nginx",
		}),
	})
	if err != nil || len(containers) == 0 {
		return 0, 0
	}

	logs, err := cli.ContainerLogs(ctx, containers[0].ID, container.LogsOptions{
		ShowStdout: true,
		Since:      checkWindow,
		// 时间过滤优先于行数：packet-up 模式每包一个 POST，2 分钟窗口内
		// 高流量可能远超固定 Tail 行数，固定行数会截断窗口早期、漏检故障。
	})
	if err != nil {
		return 0, 0
	}
	defer logs.Close()

	// Docker 容器在 tty=false 时返回 multiplexed 流（8 字节帧头），必须用
	// stdcopy.StdCopy 解码。直接 bufio.NewScanner 会读到二进制帧头，正则无法匹配。
	// 解码结果经 lineWriter 流式逐行处理：nginx 容器 stdout 是全部请求的
	// access log（不只 /proxy/query），高流量下 2 分钟窗口可达数十 MB，
	// 不整窗缓冲进内存再扫描。
	w := &lineWriter{process: func(line string) {
		if !strings.Contains(line, "POST /proxy/query/") {
			return
		}
		m := xhttpPostRE.FindStringSubmatch(line)
		if m == nil {
			return
		}
		total++
		if m[1] == "400" {
			bad++
		}
	}}
	if _, err := stdcopy.StdCopy(w, io.Discard, logs); err != nil {
		return 0, 0
	}
	return total, bad
}

// lineWriter 把写入的字节流按行拆分后回调,用于流式消费日志——内存占用
// 与最长单行同阶,而非整个日志窗口。超过 maxLineLen 仍未出现换行的数据
// 视为异常超长行直接丢弃(nginx access log 行长受 large_client_header_buffers
// 约束,正常远达不到该上限),避免恶意/损坏数据无限撑大缓冲。
const lineWriterMaxLine = 256 * 1024

type lineWriter struct {
	process func(line string)
	buf     []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			if len(w.buf) > lineWriterMaxLine {
				w.buf = w.buf[:0]
			}
			return len(p), nil
		}
		line := string(w.buf[:i])
		// 消费已处理前缀。src/dst 重叠且 src 在前,memmove 语义安全,
		// 底层数组复用,摊销 O(1)。
		w.buf = append(w.buf[:0], w.buf[i+1:]...)
		w.process(line)
	}
}
