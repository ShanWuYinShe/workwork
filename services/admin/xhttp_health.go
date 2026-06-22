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
	threshold      = 3
)

func xhttpHealthChecker(ctx context.Context, cli *client.Client) {
	var lastRestart time.Time
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(lastRestart) < cooldownPeriod {
				continue
			}
			total, bad := countXHTTPPosts(ctx, cli)
			if total >= threshold && total == bad {
				log.Printf("[xhttp-health] xhttp transport broken: %d/%d POSTs returned 400, restarting xray", bad, total)
				if err := restartXray(ctx, cli); err != nil {
					log.Printf("[xhttp-health] restart failed: %v", err)
					continue
				}
				lastRestart = time.Now()
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
		Tail:       "200",
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
