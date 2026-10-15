package accounts

import (
	"admin/internal/proxy"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// 本文件覆盖 xray client 台账的落盘语义(此前该存储的读写路径无任何测试)。
// 断言一律回读真实文件,验证持久化结果而非调用方缓存的内存状态。

// useTempXrayClientsStore 把存储路径指向临时目录下的嵌套子目录,返回该路径。
// 嵌套子目录顺带锁住"写入目录取自 xrayClientsPath"这一修复:此前 MkdirAll
// 硬编码 /app/data,路径重定向后写入会落到旧目录(非 root 环境直接失败)。
func useTempXrayClientsStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "xray-clients.json")
	prev := xrayClientsPath
	xrayClientsPath = path
	t.Cleanup(func() { xrayClientsPath = prev })
	return path
}

func readStoredClients(t *testing.T, path string) []xrayClientEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store %s: %v", path, err)
	}
	var entries []xrayClientEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal store %s: %v", path, err)
	}
	return entries
}

func seedClients(t *testing.T, entries ...xrayClientEntry) string {
	t.Helper()
	path := useTempXrayClientsStore(t)
	for _, e := range entries {
		if err := addXrayClient(e); err != nil {
			t.Fatalf("seed %s: %v", e.ID, err)
		}
	}
	return path
}

// TestAddXrayClientPersistsEntry 校验新增客户端真正落盘,内容与权限正确。
func TestAddXrayClientPersistsEntry(t *testing.T) {
	want := xrayClientEntry{ID: "11111111-2222-3333-4444-555555555555", Owner: "alice", Email: "alice"}
	path := seedClients(t, want)

	got := readStoredClients(t, path)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("存储内容不符: %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	// UUID 即代理访问凭据:台账不得对同组/其他用户可读。
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("台账权限应为 0600,实际 %o", perm)
	}
}

// TestAddXrayClientRejectsDuplicateUUID 校验 UUID 唯一性由存储层兜底。
func TestAddXrayClientRejectsDuplicateUUID(t *testing.T) {
	entry := xrayClientEntry{ID: "dup-uuid", Owner: "alice", Email: "alice"}
	path := seedClients(t, entry)

	if err := addXrayClient(entry); !errors.Is(err, errDuplicateUUID) {
		t.Fatalf("重复 UUID 应返回 errDuplicateUUID,实际 %v", err)
	}
	if got := readStoredClients(t, path); len(got) != 1 {
		t.Fatalf("被拒绝的重复写入不应改动台账: %#v", got)
	}
}

// TestAddXrayClientIfOwnerUnique 校验"每人一个 client"的归属约束与 UUID 唯一性的组合。
func TestAddXrayClientIfOwnerUnique(t *testing.T) {
	first := xrayClientEntry{ID: "uuid-alice-1", Owner: "alice", Email: "alice"}
	path := seedClients(t)

	added, err := addXrayClientIfOwnerUnique(first)
	if err != nil || !added {
		t.Fatalf("首次添加应成功: added=%v err=%v", added, err)
	}

	added, err = addXrayClientIfOwnerUnique(xrayClientEntry{ID: "uuid-alice-2", Owner: "alice", Email: "alice"})
	if err != nil || added {
		t.Fatalf("同 owner 再加应静默拒绝(不报错): added=%v err=%v", added, err)
	}
	if got := readStoredClients(t, path); len(got) != 1 {
		t.Fatalf("被拒绝的添加不应落盘: %#v", got)
	}

	// 同 UUID 换 owner:owner 检查放行后必须被 UUID 唯一性拦下,否则同一身份出现两行台账。
	added, err = addXrayClientIfOwnerUnique(xrayClientEntry{ID: first.ID, Owner: "bob", Email: "bob"})
	if !errors.Is(err, errDuplicateUUID) || added {
		t.Fatalf("重复 UUID 应返回 errDuplicateUUID: added=%v err=%v", added, err)
	}
}

// TestDeleteXrayClientIfOwner 校验跨用户删除必须被拒、admin(空 ownerFilter)可删任意。
func TestDeleteXrayClientIfOwner(t *testing.T) {
	alice := xrayClientEntry{ID: "uuid-alice", Owner: "alice", Email: "alice"}
	bob := xrayClientEntry{ID: "uuid-bob", Owner: "bob", Email: "bob"}
	path := seedClients(t, alice, bob)

	if deleted, err := deleteXrayClientIfOwner(alice.ID, "bob"); !errors.Is(err, errForbidden) || deleted {
		t.Fatalf("跨 owner 删除必须失败: deleted=%v err=%v", deleted, err)
	}
	if got := readStoredClients(t, path); len(got) != 2 {
		t.Fatalf("越权删除不得改动台账: %#v", got)
	}

	if deleted, err := deleteXrayClientIfOwner("uuid-missing", ""); !errors.Is(err, errNotFound) || deleted {
		t.Fatalf("删除不存在的 client 应返回 errNotFound: deleted=%v err=%v", deleted, err)
	}

	if deleted, err := deleteXrayClientIfOwner(alice.ID, ""); err != nil || !deleted {
		t.Fatalf("admin 删除应成功: deleted=%v err=%v", deleted, err)
	}
	if got := readStoredClients(t, path); len(got) != 1 || got[0].ID != bob.ID {
		t.Fatalf("admin 删除后台账错误: %#v", got)
	}

	if deleted, err := deleteXrayClientIfOwner(bob.ID, "bob"); err != nil || !deleted {
		t.Fatalf("本人删除应成功: deleted=%v err=%v", deleted, err)
	}
	if got := readStoredClients(t, path); len(got) != 0 {
		t.Fatalf("删除后应为空台账: %#v", got)
	}
}

// TestDeleteXrayClientsByOwner 校验删除面板用户时联动撤销其全部代理身份。
func TestDeleteXrayClientsByOwner(t *testing.T) {
	path := seedClients(t,
		xrayClientEntry{ID: "uuid-a1", Owner: "alice", Email: "alice"},
		xrayClientEntry{ID: "uuid-a2", Owner: "alice", Email: "alice"},
		xrayClientEntry{ID: "uuid-b1", Owner: "bob", Email: "bob"},
	)

	removed, err := deleteXrayClientsByOwner("alice")
	if err != nil || removed != 2 {
		t.Fatalf("应撤销 alice 的 2 个 client: removed=%d err=%v", removed, err)
	}
	got := readStoredClients(t, path)
	if len(got) != 1 || got[0].ID != "uuid-b1" {
		t.Fatalf("撤销后台账错误: %#v", got)
	}

	removed, err = deleteXrayClientsByOwner("carol")
	if err != nil || removed != 0 {
		t.Fatalf("无匹配 owner 应返回 0 且不报错: removed=%d err=%v", removed, err)
	}
}

// TestAddXrayClientConcurrentWritesAllEntries 校验读-改-写在同一把锁内完成:
// 并发新增不得丢条目(避免 last-writer-wins)。配合 -race 一并覆盖数据竞争。
func TestAddXrayClientConcurrentWritesAllEntries(t *testing.T) {
	const n = 16
	path := seedClients(t)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("uuid-%02d", i)
			if err := addXrayClient(xrayClientEntry{ID: id, Owner: id, Email: id}); err != nil {
				t.Errorf("并发新增 %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, e := range readStoredClients(t, path) {
		seen[e.ID] = true
	}
	if len(seen) != n {
		t.Fatalf("并发新增丢条目: 期望 %d 条,实际 %d 条", n, len(seen))
	}
}

// TestEnsureXrayClientsKeepsRevokedStore 锁定"以文件是否存在判断首启"的吊销语义:
// 管理员删光全部 client 后文件是空集,重启不得按 env 重新播种(否则被吊销的
// 代理身份复活)。
func TestEnsureXrayClientsKeepsRevokedStore(t *testing.T) {
	path := useTempXrayClientsStore(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("[]"), 0o600); err != nil {
		t.Fatalf("write empty store: %v", err)
	}
	t.Setenv("XRAY_CLIENT_IDS", "uuid-should-not-revive")
	t.Setenv("AUTH_USER", "alice")

	if err := EnsureXrayClients(); err != nil {
		t.Fatalf("EnsureXrayClients: %v", err)
	}
	if got := readStoredClients(t, path); len(got) != 0 {
		t.Fatalf("空集被重新播种,被吊销的身份复活: %#v", got)
	}
}

// TestEnsureXrayClientsSeedsFromEnvOnFirstRun 校验首启从 XRAY_CLIENT_IDS 播种
// (逗号项去空白、空项跳过),owner/email 取 AUTH_USER。
func TestEnsureXrayClientsSeedsFromEnvOnFirstRun(t *testing.T) {
	// 容器内 legacy 配置已存在时 EnsureXrayClients 走迁移分支,env 播种不适用。
	if _, err := os.Stat(proxy.XrayConfigPath); err == nil {
		t.Skipf("%s 存在,EnsureXrayClients 会优先迁移 legacy 配置,跳过 env 播种用例", proxy.XrayConfigPath)
	}
	path := useTempXrayClientsStore(t)
	t.Setenv("XRAY_CLIENT_IDS", " uuid-1 ,,uuid-2 ")
	t.Setenv("AUTH_USER", "alice")

	if err := EnsureXrayClients(); err != nil {
		t.Fatalf("EnsureXrayClients: %v", err)
	}
	got := readStoredClients(t, path)
	if len(got) != 2 {
		t.Fatalf("应播种 2 个 client(空项跳过): %#v", got)
	}
	for i, wantID := range []string{"uuid-1", "uuid-2"} {
		if got[i].ID != wantID {
			t.Fatalf("第 %d 个 client ID 应为 %s: %#v", i+1, wantID, got[i])
		}
		if got[i].Owner != "alice" || got[i].Email != "alice" {
			t.Fatalf("owner/email 应取 AUTH_USER: %#v", got[i])
		}
	}
}
