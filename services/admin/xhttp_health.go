package main

import (
	"bufio"
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

func xhttpHealthChecker(ctx context.Context, cli *client.Client) {
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
				if err := restartXray(ctx, cli); err != nil {
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
		Filters: composeProjectFilters(filters.KeyValuePair{
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
		// 去掉 Tail 后窗口内日志全量返回，5 分钟低频检查的 stdout buffer
		// 开销可接受（正确性优先）。
	})
	if err != nil {
		return 0, 0
	}
	defer logs.Close()

	// Docker 容器在 tty=false 时返回 multiplexed 流（8 字节帧头），必须用
	// stdcopy.StdCopy 解码。直接 bufio.NewScanner 会读到二进制帧头，正则无法匹配。
	var stdout bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, io.Discard, logs); err != nil {
		return 0, 0
	}

	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 64*1024), 256*1024) // 放大行长上限，避免超长 URL/UA 截断
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "POST /proxy/query/") {
			continue
		}
		m := xhttpPostRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		total++
		if m[1] == "400" {
			bad++
		}
	}
	return total, bad
}
