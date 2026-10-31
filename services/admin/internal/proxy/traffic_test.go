package proxy

import (
	"testing"
)

// TestParseStatsQueryOutput 计数器名解析:user>>><email>>>uplink|downlink;
// 非 user 前缀(inbound/outbound)与畸形名跳过,负值/非法值跳过。
func TestParseStatsQueryOutput(t *testing.T) {
	// 键名 "stat" 单数形态(xray 26.6.27 实测)。
	out := `{"stat":[
		{"name":"user>>>alice>>>traffic>>>uplink","value":"1000"},
		{"name":"user>>>alice>>>traffic>>>downlink","value":"2000"},
		{"name":"user>>>bob>>>traffic>>>uplink","value":"5"},
		{"name":"user>>>carol>>>uplink","value":"42"},
		{"name":"inbound>>>xhttp-in>>>traffic>>>uplink","value":"9999"},
		{"name":"user>>>weird","value":"1"},
		{"name":"user>>>carol>>>traffic>>>uplink","value":"-3"},
		{"name":"user>>>carol>>>traffic>>>uplink","value":7}
	]}`

	// 复数 "stats" 键名形态同样支持。
	out2 := `{"stats":[
		{"name":"user>>>dave>>>traffic>>>uplink","value":"9"}
	]}`
	cur2, err := parseStatsQueryOutput([]byte(out2))
	if err != nil {
		t.Fatal(err)
	}
	if cur2["dave"].Up != 9 {
		t.Fatalf("dave = %+v", cur2["dave"])
	}
	cur, err := parseStatsQueryOutput([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 3 {
		t.Fatalf("users = %d, want 3 (alice,bob,carol)", len(cur))
	}
	if cur["alice"].Up != 1000 || cur["alice"].Down != 2000 {
		t.Fatalf("alice = %+v", cur["alice"])
	}
	if cur["bob"].Up != 5 {
		t.Fatalf("bob = %+v", cur["bob"])
	}
	// carol:非 traffic 类别的 3 段名、负值跳过,最后 JSON number 7 生效。
	if cur["carol"].Up != 7 {
		t.Fatalf("carol = %+v", cur["carol"])
	}
}

// TestAccumulate 正常增量与 xray 重启清零(counter 回退)两种路径;
// 消失的用户基线同步丢弃。
func TestAccumulate(t *testing.T) {
	savedTotals, savedBaseline := trafficTotals, trafficBaseline
	t.Cleanup(func() { trafficTotals, trafficBaseline = savedTotals, savedBaseline })
	trafficTotals = map[string]userTraffic{}
	trafficBaseline = map[string]int64{}

	// 第一轮:alice up=100(基线从零起,全额入账)。
	if changed := accumulate(map[string]userTraffic{"alice": {Up: 100, Down: 50}}); !changed {
		t.Fatal("first poll should record traffic")
	}
	if trafficTotals["alice"].Up != 100 || trafficTotals["alice"].Down != 50 {
		t.Fatalf("totals after first poll: %+v", trafficTotals["alice"])
	}
	// 第二轮:累计值涨到 150/60 → 增量 50/10。
	accumulate(map[string]userTraffic{"alice": {Up: 150, Down: 60}})
	if trafficTotals["alice"].Up != 150 || trafficTotals["alice"].Down != 60 {
		t.Fatalf("totals after second poll: %+v", trafficTotals["alice"])
	}
	// xray 重启清零:cur < last → delta = cur(全额)。
	accumulate(map[string]userTraffic{"alice": {Up: 30, Down: 0}})
	if trafficTotals["alice"].Up != 180 || trafficTotals["alice"].Down != 60 {
		t.Fatalf("totals after restart-reset: %+v", trafficTotals["alice"])
	}
	// 用户消失:基线丢弃,totals 保留(历史流量仍可见)。
	accumulate(map[string]userTraffic{})
	if _, ok := trafficBaseline["alice>>>uplink"]; ok {
		t.Fatal("baseline of removed user should be dropped")
	}
	if trafficTotals["alice"].Up != 180 {
		t.Fatal("totals of removed user should be kept for display")
	}
}

// TestSetTrafficEmailAliasesMigratesHistory 锁定台账 email 规范化（登录用户名
// → 客户端 UUID）时历史流量续接：旧键的累计值与基线必须搬到新键，否则
// 迁移后流量展示从零开始、旧键变成客户端列表里不存在的幽灵行。
func TestSetTrafficEmailAliasesMigratesHistory(t *testing.T) {
	trafficMu.Lock()
	prevTotals, prevBaseline := trafficTotals, trafficBaseline
	prevLoaded, prevAliases := trafficLoaded, trafficEmailAliases
	trafficTotals = map[string]userTraffic{"alice": {Up: 10, Down: 20}}
	trafficBaseline = map[string]int64{"alice>>>uplink": 10, "alice>>>downlink": 20}
	trafficLoaded = true
	trafficEmailAliases = nil
	trafficMu.Unlock()
	t.Cleanup(func() {
		trafficMu.Lock()
		trafficTotals, trafficBaseline = prevTotals, prevBaseline
		trafficLoaded, trafficEmailAliases = prevLoaded, prevAliases
		trafficMu.Unlock()
	})

	SetTrafficEmailAliases(map[string]string{"alice": "uuid-alice"})
	snap := TrafficSnapshot()
	if _, ok := snap["alice"]; ok {
		t.Fatalf("old email key should be gone: %+v", snap)
	}
	if got := snap["uuid-alice"]; got.Up != 10 || got.Down != 20 {
		t.Fatalf("totals not migrated: %+v", got)
	}
	trafficMu.Lock()
	defer trafficMu.Unlock()
	if trafficBaseline["uuid-alice>>>uplink"] != 10 || trafficBaseline["uuid-alice>>>downlink"] != 20 {
		t.Fatalf("baseline not migrated: %+v", trafficBaseline)
	}
	if _, ok := trafficBaseline["alice>>>uplink"]; ok {
		t.Fatalf("old baseline key should be gone: %+v", trafficBaseline)
	}
}
