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
	"admin/internal/runtime"
	"admin/internal/xray"
	"context"
	"encoding/json"
	"errors"
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

// TrafficTracker 是流量统计的唯一状态 owner。路径由 Layout 注入，不再有
// traffic-state.json 的包级字面量与可变全局。
type TrafficTracker struct {
	path string
	mu   sync.Mutex

	totals   map[string]userTraffic
	baseline map[string]int64
	loaded   bool
	// 连续失败一次性告警:旧 xray 镜像无 stats API 时避免每分钟刷屏。
	failLogged bool
	// aliases 是客户端台账规范化产生的"旧 email → 新 email"映射
	// （登录用户名 → 客户端 UUID）。在状态加载之后应用一次，把历史累计值
	// 续接到新键。
	aliases map[string]string
}

// NewTrafficTracker 基于 Layout 构造流量统计器；首次采集时才读取状态文件。
func NewTrafficTracker(layout runtime.Layout) *TrafficTracker {
	return &TrafficTracker{
		path:   layout.TrafficStatePath(),
		totals: map[string]userTraffic{},
	}
}

// SetEmailAliases 注册台账规范化产生的 email 迁移映射。可在流量状态
// 加载前或加载后调用：未加载时记住，加载时应用；已加载时立即应用。
func (t *TrafficTracker) SetEmailAliases(aliases map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.aliases = aliases
	if t.loaded {
		t.applyEmailAliasesLocked()
	}
}

// applyEmailAliasesLocked 把旧 email 键下的累计/基线合并到新键。
// 调用方必须持有 t.mu。
func (t *TrafficTracker) applyEmailAliasesLocked() {
	for oldEmail, newEmail := range t.aliases {
		if oldEmail == "" || newEmail == "" || oldEmail == newEmail {
			continue
		}
		if v, ok := t.totals[oldEmail]; ok {
			cur := t.totals[newEmail]
			cur.Up += v.Up
			cur.Down += v.Down
			t.totals[newEmail] = cur
			delete(t.totals, oldEmail)
		}
		if t.baseline != nil {
			for _, dir := range []string{"uplink", "downlink"} {
				oldKey := oldEmail + ">>>" + dir
				newKey := newEmail + ">>>" + dir
				if v, ok := t.baseline[oldKey]; ok {
					t.baseline[newKey] += v
					delete(t.baseline, oldKey)
				}
			}
		}
	}
}

// loadLocked 首次调用时从磁盘恢复(进程内只加载一次)。调用方必须持有 t.mu。
// 损坏文件已被 LoadJSONFile 隔离（原字节保留在 .corrupt），统计是旁路
// 数据，可以从零重建；读错误（权限/IO）不能当空表继续，否则下一轮 save
// 会把原有累计值覆盖掉——返回 error，由采集器重试。
func (t *TrafficTracker) loadLocked() error {
	if t.loaded {
		return nil
	}
	var st trafficState
	if err := platform.LoadJSONFile(t.path, &st); err != nil {
		if !errors.Is(err, platform.ErrCorruptJSON) {
			return err
		}
		log.Printf("WARNING: traffic state corrupt, starting from zero (%v)", err)
	}
	t.loaded = true
	if st.Baseline != nil {
		t.baseline = st.Baseline
	}
	if st.Totals != nil {
		t.totals = st.Totals
	}
	t.applyEmailAliasesLocked()
	return nil
}

// saveLocked 持锁落盘。失败只记日志:丢一轮基线最坏情况是
// xray 下次重启后少算一个轮询间隔的增量。调用方必须持有 t.mu。
func (t *TrafficTracker) saveLocked() {
	if !t.loaded {
		return
	}
	st := trafficState{Baseline: t.baseline, Totals: t.totals}
	if err := platform.SaveJSONFile(t.path, st); err != nil {
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
		v := out[email]
		switch parts[3] {
		case "uplink":
			v.Up = val
		case "downlink":
			v.Down = val
		default:
			continue
		}
		out[email] = v
	}
	return out, nil
}

// accumulate 把本轮快照并入 totals:正常 delta = cur-last;xray 重启清零
// (cur < last)时 delta = cur;新出现的计数器从 0 起算。返回是否发生变化。
// 调用方必须持有 t.mu。
func (t *TrafficTracker) accumulate(cur map[string]userTraffic) bool {
	changed := false
	for email, v := range cur {
		last := t.baselineEntry(email + ">>>uplink")
		deltaUp := v.Up - last
		if v.Up < last {
			deltaUp = v.Up
		}
		lastDown := t.baselineEntry(email + ">>>downlink")
		deltaDown := v.Down - lastDown
		if v.Down < lastDown {
			deltaDown = v.Down
		}
		if deltaUp == 0 && deltaDown == 0 {
			continue
		}
		tot := t.totals[email]
		tot.Up += deltaUp
		tot.Down += deltaDown
		t.totals[email] = tot
		changed = true
	}
	// 基线整体替换(消失的计数器=对应用户已删,基线同步丢弃)。
	t.baseline = map[string]int64{}
	for email, v := range cur {
		t.baseline[email+">>>uplink"] = v.Up
		t.baseline[email+">>>downlink"] = v.Down
	}
	return changed
}

func (t *TrafficTracker) baselineEntry(name string) int64 {
	if t.baseline == nil {
		return 0
	}
	return t.baseline[name]
}

// Snapshot 返回当前累计流量的副本(按 email),供 API 展示。
func (t *TrafficTracker) Snapshot() map[string]userTraffic {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]userTraffic, len(t.totals))
	for k, v := range t.totals {
		out[k] = v
	}
	return out
}

// Start 每分钟拉取一次 xray per-user 流量,随 ctx 退出。
func (t *TrafficTracker) Start(ctx context.Context, cli *client.Client) {
	runPoll := func() {
		// 状态文件读失败时不要继续查询/落盘：下一轮 save 会用空表覆盖。
		// 每一轮都重试加载，文件恢复后自动继续。
		t.mu.Lock()
		loadErr := t.loadLocked()
		loaded := t.loaded
		t.mu.Unlock()
		if !loaded {
			t.logFailure("load state", loadErr)
			return
		}

		pollCtx, cancel := context.WithTimeout(ctx, trafficExecTimeout)
		defer cancel()
		xrayID, err := platform.FindContainer(pollCtx, cli, "xray")
		if err != nil {
			t.logFailure("find xray container", err)
			return
		}
		out, err := platform.ContainerExecOutput(pollCtx, cli, xrayID,
			"xray", "api", "statsquery", "--server="+xray.StatsAPIListen)
		if err != nil {
			t.logFailure("statsquery", err)
			return
		}
		cur, err := parseStatsQueryOutput([]byte(out))
		if err != nil {
			t.logFailure("parse stats", err)
			return
		}
		t.mu.Lock()
		changed := t.accumulate(cur)
		if changed {
			t.saveLocked()
		}
		failLogged := t.failLogged
		t.failLogged = false
		t.mu.Unlock()
		if failLogged {
			log.Printf("traffic collector recovered")
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

// logFailure 一次性告警连续失败(恢复后重置);统计是旁路能力,
// 失败不影响面板主功能。
func (t *TrafficTracker) logFailure(what string, err error) {
	t.mu.Lock()
	first := !t.failLogged
	t.failLogged = true
	t.mu.Unlock()
	if first {
		log.Printf("WARNING: traffic collector %s failed (will retry silently): %v", what, err)
	}
}

// API 返回按用户累计的代理流量(admin)。只读端点,无审计。
func (t *TrafficTracker) API() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		snapshot := t.Snapshot()
		type row struct {
			// email 现在是客户端 UUID（xray 统计键），不再是登录用户名。
			Email string `json:"email"`
			Up    int64  `json:"up"`
			Down  int64  `json:"down"`
		}
		rows := make([]row, 0, len(snapshot))
		for email, v := range snapshot {
			rows = append(rows, row{Email: email, Up: v.Up, Down: v.Down})
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].Email < rows[j].Email
		})
		platform.WriteJSON(w, map[string]any{"traffic": rows, "collected_at": time.Now().UTC().Format(time.RFC3339)})
	}
}
