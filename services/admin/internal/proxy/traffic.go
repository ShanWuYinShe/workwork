package proxy

// per-user 流量统计:architecture.md 曾承诺的能力。数据源是 xray 内置
// StatsService(模板已开 statsUserUplink/Downlink,计数器名
// user>>><email>>>uplink|downlink,email = 面板用户名);admin 每分钟经
// docker exec 调 `xray api statsquery`(复用 docker-proxy 的 exec 白名单,
// 不引入 gRPC 依赖)取**自 xray 启动起的累计值**,与上次快照做增量后累加
// 进 totals——xray 重启清零、admin 重启丢内存两种情况都被落盘的快照基线
// 兜住:admin 重启后从 traffic-state.json 恢复基线与 totals,增量连续。

import (
	"admin/internal/platform"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/client"
)

const (
	trafficPollEvery   = time.Minute
	trafficExecTimeout = 20 * time.Second
)

// userTraffic 是单用户累计流量(字节)。
type userTraffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// trafficState 是落盘的状态文件:baseline 是上次快照的原始累计计数器,
// totals 是跨重启累加的总流量。
type trafficState struct {
	Baseline map[string]int64       `json:"baseline"`
	Totals   map[string]userTraffic `json:"totals"`
}

var (
	trafficMu       sync.Mutex
	trafficTotals   = map[string]userTraffic{}
	trafficBaseline map[string]int64
	trafficLoaded   bool
	// 连续失败一次性告警:旧 xray 镜像无 stats API 时避免每分钟刷屏。
	trafficFailLogged bool
)

// trafficStatePath var 供测试替换。
var trafficStatePath = "/app/data/traffic-state.json"

// loadTrafficState 首次调用时从磁盘恢复(进程内只加载一次)。
func loadTrafficState() {
	if trafficLoaded {
		return
	}
	trafficLoaded = true
	var st trafficState
	platform.LoadJSONFile(trafficStatePath, &st)
	if st.Baseline != nil {
		trafficBaseline = st.Baseline
	}
	if st.Totals != nil {
		trafficTotals = st.Totals
	}
}

// saveTrafficStateLocked 持锁落盘。失败只记日志:丢一轮基线最坏情况是
// xray 下次重启后少算一个轮询间隔的增量。
func saveTrafficStateLocked() {
	st := trafficState{Baseline: trafficBaseline, Totals: trafficTotals}
	if err := platform.SaveJSONFile(trafficStatePath, st); err != nil {
		log.Printf("WARNING: persist traffic state: %v", err)
	}
}

// statsQueryEntry 是 statsquery 输出数组的一行。
type statsQueryEntry struct {
	Name  string      `json:"name"`
	Value json.Number `json:"value"`
}

// parseStatsQueryOutput 解析 `xray api statsquery` 的 JSON 输出,只保留
// user>>> 前缀计数器,返回 email → (up, down) 累计值。
func parseStatsQueryOutput(data []byte) (map[string]userTraffic, error) {
	// statsquery 的响应键名两种形态都有:xray 26.6.27 实测返回 "stat"(单数),
	// 部分版本/文档为 "stats"。两个都读,取非空者。
	var resp struct {
		Stat  []statsQueryEntry `json:"stat"`
		Stats []statsQueryEntry `json:"stats"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	entries := resp.Stats
	if len(resp.Stat) > 0 {
		entries = resp.Stat
	}
	out := map[string]userTraffic{}
	for _, s := range entries {
		// 计数器名形如 user>>><email>>>traffic>>>uplink|downlink(xray 26.x
		// 实测为 4 段,中间是类别 traffic;按 3 段解析会全部跳过,流量恒为空)。
		if !strings.HasPrefix(s.Name, "user>>>") {
			continue
		}
		parts := strings.Split(s.Name, ">>>")
		if len(parts) != 4 || parts[2] != "traffic" {
			continue
		}
		email := parts[1]
		val, err := strconv.ParseInt(string(s.Value), 10, 64)
		if err != nil || val < 0 {
			continue
		}
		t := out[email]
		switch parts[3] {
		case "uplink":
			t.Up = val
		case "downlink":
			t.Down = val
		default:
			continue
		}
		out[email] = t
	}
	return out, nil
}

// accumulate 把本轮快照并入 totals:正常 delta = cur-last;xray 重启清零
// (cur < last)时 delta = cur;新出现的计数器从 0 起算。返回是否发生变化。
func accumulate(cur map[string]userTraffic) bool {
	changed := false
	for email, t := range cur {
		last := trafficBaselineEntry(email + ">>>uplink")
		deltaUp := t.Up - last
		if t.Up < last {
			deltaUp = t.Up
		}
		lastDown := trafficBaselineEntry(email + ">>>downlink")
		deltaDown := t.Down - lastDown
		if t.Down < lastDown {
			deltaDown = t.Down
		}
		if deltaUp == 0 && deltaDown == 0 {
			continue
		}
		tot := trafficTotals[email]
		tot.Up += deltaUp
		tot.Down += deltaDown
		trafficTotals[email] = tot
		changed = true
	}
	// 基线整体替换(消失的计数器=对应用户已删,基线同步丢弃)。
	trafficBaseline = map[string]int64{}
	for email, t := range cur {
		trafficBaseline[email+">>>uplink"] = t.Up
		trafficBaseline[email+">>>downlink"] = t.Down
	}
	return changed
}

func trafficBaselineEntry(name string) int64 {
	if trafficBaseline == nil {
		return 0
	}
	return trafficBaseline[name]
}

// TrafficSnapshot 返回当前累计流量的副本(按 email),供 API 展示。
func TrafficSnapshot() map[string]userTraffic {
	trafficMu.Lock()
	defer trafficMu.Unlock()
	out := make(map[string]userTraffic, len(trafficTotals))
	for k, v := range trafficTotals {
		out[k] = v
	}
	return out
}

// StartTrafficCollector 每分钟拉取一次 xray per-user 流量,随 ctx 退出。
func StartTrafficCollector(ctx context.Context, cli *client.Client) {
	trafficMu.Lock()
	loadTrafficState()
	trafficMu.Unlock()

	runPoll := func() {
		pollCtx, cancel := context.WithTimeout(ctx, trafficExecTimeout)
		defer cancel()
		xrayID, err := platform.FindContainer(pollCtx, cli, "xray")
		if err != nil {
			logTrafficFailure("find xray container", err)
			return
		}
		out, err := platform.ContainerExecOutput(pollCtx, cli, xrayID,
			"xray", "api", "statsquery", "--server=127.0.0.1:10085")
		if err != nil {
			logTrafficFailure("statsquery", err)
			return
		}
		cur, err := parseStatsQueryOutput([]byte(out))
		if err != nil {
			logTrafficFailure("parse stats", err)
			return
		}
		trafficMu.Lock()
		changed := accumulate(cur)
		if changed {
			saveTrafficStateLocked()
		}
		trafficMu.Unlock()
		if trafficFailLogged {
			log.Printf("traffic collector recovered")
			trafficFailLogged = false
		}
	}
	runPoll()
	ticker := time.NewTicker(trafficPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPoll()
		}
	}
}

// logTrafficFailure 一次性告警连续失败(恢复后重置);统计是旁路能力,
// 失败不影响面板主功能。
func logTrafficFailure(what string, err error) {
	if !trafficFailLogged {
		log.Printf("WARNING: traffic collector %s failed (will retry silently): %v", what, err)
		trafficFailLogged = true
	}
}

// ApiTraffic 返回按用户累计的代理流量(admin)。只读端点,无审计。
func ApiTraffic() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot := TrafficSnapshot()
		type row struct {
			User string `json:"user"`
			Up   int64  `json:"up"`
			Down int64  `json:"down"`
		}
		rows := make([]row, 0, len(snapshot))
		for user, t := range snapshot {
			rows = append(rows, row{User: user, Up: t.Up, Down: t.Down})
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].User < rows[j].User
		})
		platform.WriteJSON(w, map[string]any{"traffic": rows, "collected_at": time.Now().UTC().Format(time.RFC3339)})
	}
}
