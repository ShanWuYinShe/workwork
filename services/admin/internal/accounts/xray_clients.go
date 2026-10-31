package accounts

import (
	"admin/internal/platform"
	"admin/internal/proxy"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	errForbidden     = errors.New("forbidden: not your client")
	errNotFound      = errors.New("client not found")
	errDuplicateUUID = errors.New("client with this UUID already exists")
)

// Xray clients are stored independently from the generated xray.json.
// This keeps user-id data durable across template/config changes.

// xrayClientEntry 是代理身份台账。
// Owner 是负责该客户端的面板用户（也是 nginx Basic Auth 的认证身份）；
// Email 恒等于客户端自己的 UUID，是 Xray 的统计键。
//
// 为什么 Email 不能再取登录用户名：Xray 的 VLESS validator 要求同一入站内
// 非空 email 唯一（比较前 lower-case），用户建第二个客户端时会产生重复
// email，整份配置无法加载（实测 xray 26.6.27 报 "User admin already
// exists."）。客户端与登录用户是两类对象：一个用户可以有多个客户端。
type xrayClientEntry struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	Email string `json:"email"`
}

var (
	xrayClientsPath  = "/app/data/xray-clients.json"
	xrayClientsMu    sync.Mutex
	xrayEmailAliases = map[string]string{}

	// userLifecycleMu 串行化"删除面板用户"与"新增 Xray 客户端"。
	// 两个操作分别写 users.json 与 xray-clients.json；不加锁时，删除流程
	// 结束后在途的 add 仍可能把 proxy 凭证/客户端写回去，导致"删除用户 =
	// 撤销代理身份"不成立。只在 accounts 包内使用。
	userLifecycleMu sync.Mutex
)

// readXrayClientsNoLock assumes the caller already holds xrayClientsMu.
func readXrayClientsNoLock() ([]xrayClientEntry, error) {
	data, err := os.ReadFile(xrayClientsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var clients []xrayClientEntry
	if err := json.Unmarshal(data, &clients); err != nil {
		return nil, err
	}
	return clients, nil
}

func writeXrayClientsNoLock(clients []xrayClientEntry) error {
	// 目录取自 xrayClientsPath 而非字面量 /app/data:与读路径同源。此前硬编码
	// 字面量,路径变量被改/被测试重定向后写入会落在旧目录(或直接失败)。
	if err := os.MkdirAll(filepath.Dir(xrayClientsPath), 0755); err != nil {
		return err
	}
	// nil 切片序列化成 null；统一写成 [] 让"空台账"在文件里也是明确的空集。
	if clients == nil {
		clients = []xrayClientEntry{}
	}
	data, err := json.MarshalIndent(clients, "", "  ")
	if err != nil {
		return err
	}
	return platform.AtomicWriteFile(xrayClientsPath, data, 0600)
}

// normalizeXrayClientEntry 收敛单条台账：UUID 小写、owner 非空、email=UUID。
func normalizeXrayClientEntry(c xrayClientEntry) xrayClientEntry {
	c.ID = strings.ToLower(strings.TrimSpace(c.ID))
	c.Owner = strings.TrimSpace(c.Owner)
	if c.Owner == "" {
		c.Owner = c.ID
	}
	c.Email = c.ID
	return c
}

// normalizeXrayClientEntries 规范化整份台账，并返回"旧 email → 新 email"的
// 迁移映射，供流量统计续接历史（xray 统计键是 email，规范化会改键）。
//
// 返回的 aliases 会剔除歧义项：如果多个客户端历史上共用同一个 email
// （正是本次修复前的 bug 形态），无法判断流量属于谁，就不迁移。
func normalizeXrayClientEntries(clients []xrayClientEntry) ([]xrayClientEntry, map[string]string, bool) {
	emailCount := make(map[string]int, len(clients))
	for _, c := range clients {
		if e := strings.TrimSpace(c.Email); e != "" {
			emailCount[strings.ToLower(e)]++
		}
	}

	aliases := make(map[string]string)
	seen := make(map[string]bool, len(clients))
	out := make([]xrayClientEntry, 0, len(clients))
	changed := false
	for _, c := range clients {
		id := strings.ToLower(strings.TrimSpace(c.ID))
		if id == "" {
			changed = true
			continue
		}
		if seen[id] {
			// 同一 UUID 的大小写变体/重复条目：只保留第一条。
			changed = true
			continue
		}
		seen[id] = true

		oldEmail := strings.TrimSpace(c.Email)
		if oldEmail != "" && !strings.EqualFold(oldEmail, id) && emailCount[strings.ToLower(oldEmail)] == 1 {
			aliases[oldEmail] = id
		}
		owner := strings.TrimSpace(c.Owner)
		if owner == "" {
			owner = id
		}
		if c.ID != id || c.Owner != owner || c.Email != id {
			changed = true
		}
		out = append(out, xrayClientEntry{ID: id, Owner: owner, Email: id})
	}
	return out, aliases, changed
}

// clientExistsNoLock reports whether a client with the given UUID already exists.
// Caller must hold xrayClientsMu.
func clientExistsNoLock(clients []xrayClientEntry, id string) bool {
	for _, c := range clients {
		if c.ID == id {
			return true
		}
	}
	return false
}

// addXrayClient 在单次锁获取内完成 read-modify-write，避免并发 last-writer-wins。
// 不再限制同一 owner 只能有一个客户端：登录用户是 nginx 认证身份，客户端是
// 其名下的代理身份，一个用户可以创建多个。
func addXrayClient(entry xrayClientEntry) error {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	entry = normalizeXrayClientEntry(entry)
	if entry.ID == "" {
		return errors.New("client id is empty")
	}
	clients, err := readXrayClientsNoLock()
	if err != nil {
		return err
	}
	if clientExistsNoLock(clients, entry.ID) {
		return errDuplicateUUID
	}
	clients = append(clients, entry)
	return writeXrayClientsNoLock(clients)
}

// removeXrayClient 无条件删除指定 UUID（回滚路径使用：配置生成/重启失败时
// 把刚写入的条目恢复原状），返回是否真的删除了条目。
func removeXrayClient(id string) (bool, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	id = strings.ToLower(strings.TrimSpace(id))
	clients, err := readXrayClientsNoLock()
	if err != nil {
		return false, err
	}
	kept := make([]xrayClientEntry, 0, len(clients))
	removed := false
	for _, c := range clients {
		if c.ID == id {
			removed = true
			continue
		}
		kept = append(kept, c)
	}
	if !removed {
		return false, nil
	}
	return true, writeXrayClientsNoLock(kept)
}

// deleteXrayClientIfOwner 在单次锁获取内完成所有权检查和删除，返回被删条目
// （供配置生成/重启失败时回滚）。
// ownerFilter 为空表示不检查所有权（admin）；非空则只允许删除属于该 owner 的 client。
func deleteXrayClientIfOwner(id string, ownerFilter string) (xrayClientEntry, bool, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	id = strings.ToLower(strings.TrimSpace(id))
	clients, err := readXrayClientsNoLock()
	if err != nil {
		return xrayClientEntry{}, false, err
	}

	var kept []xrayClientEntry
	var deleted xrayClientEntry
	found := false
	for _, c := range clients {
		if c.ID == id {
			if ownerFilter != "" && c.Owner != ownerFilter {
				return xrayClientEntry{}, false, errForbidden
			}
			deleted = c
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		return xrayClientEntry{}, false, errNotFound
	}
	return deleted, true, writeXrayClientsNoLock(kept)
}

// deleteXrayClientsByOwner 删除某 owner 名下的全部 client，返回删除数量。
// 用于删除面板用户时联动撤销其代理身份（UUID 留存等同保留代理访问权）。
func deleteXrayClientsByOwner(owner string) (int, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	clients, err := readXrayClientsNoLock()
	if err != nil {
		return 0, err
	}
	var kept []xrayClientEntry
	removed := 0
	for _, c := range clients {
		if c.Owner == owner {
			removed++
			continue
		}
		kept = append(kept, c)
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, writeXrayClientsNoLock(kept)
}

// EnsureXrayClients initializes the data file on first run and normalizes an
// existing file (lower-case UUID, email=UUID, drop duplicate UUIDs).
//
// 以「数据文件是否存在」判断首次运行，而非集合是否为空：管理员把客户端
// 全部删除（吊销）后文件是空集，若按空集判断会在重启时重新播下 env/
// legacy 配置里的 UUID，被吊销的代理身份悄悄恢复。
func EnsureXrayClients() error {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	_, statErr := os.Stat(xrayClientsPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}

	if os.IsNotExist(statErr) {
		clients := migrateXrayClientsFromConfig()
		if len(clients) == 0 {
			ids := platform.GetEnv("XRAY_CLIENT_IDS")
			owner := platform.GetEnv("AUTH_USER")
			for _, u := range strings.Split(ids, ",") {
				u = strings.TrimSpace(u)
				if u == "" {
					continue
				}
				clients = append(clients, xrayClientEntry{ID: u, Owner: owner, Email: u})
			}
		}
		normalized, aliases, _ := normalizeXrayClientEntries(clients)
		xrayEmailAliases = aliases
		if len(normalized) == 0 {
			return nil
		}
		return writeXrayClientsNoLock(normalized)
	}

	clients, err := readXrayClientsNoLock()
	if err != nil {
		// 损坏的台账不能静默当空表：GenXrayConfig 会用空 clients 覆盖运行
		// 配置，所有代理身份一起失效。让启动失败并保留现场。
		return err
	}
	normalized, aliases, changed := normalizeXrayClientEntries(clients)
	xrayEmailAliases = aliases
	if !changed {
		return nil
	}
	log.Printf("Normalizing xray client ledger: %d entries (lower-case UUID, email=UUID, dedupe)", len(normalized))
	return writeXrayClientsNoLock(normalized)
}

// XrayEmailAliases 返回本次启动规范化时产生的"旧 email → 新 email"映射，
// 供流量统计把历史累计值续接到新键。无迁移时为空 map。
func XrayEmailAliases() map[string]string {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()
	out := make(map[string]string, len(xrayEmailAliases))
	for k, v := range xrayEmailAliases {
		out[k] = v
	}
	return out
}

func migrateXrayClientsFromConfig() []xrayClientEntry {
	data, err := os.ReadFile(proxy.XrayConfigPath)
	if err != nil {
		data = nil
	}
	if len(data) == 0 {
		return nil
	}
	var cfg struct {
		Inbounds []struct {
			Settings struct {
				Clients []xrayClientEntry `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var clients []xrayClientEntry
	for _, ib := range cfg.Inbounds {
		for _, c := range ib.Settings.Clients {
			id := strings.ToLower(strings.TrimSpace(c.ID))
			if id == "" || id == "test" || seen[id] {
				continue
			}
			seen[id] = true
			if c.Owner == "" {
				c.Owner = id
			}
			if c.Email == "" {
				c.Email = id
			}
			clients = append(clients, c)
		}
	}
	return clients
}

func ReadXrayClientMaps() ([]map[string]string, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()
	return readXrayClientMapsNoLock()
}
