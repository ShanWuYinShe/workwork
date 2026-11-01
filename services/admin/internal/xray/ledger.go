package xray

import (
	"admin/internal/runtime"
	"log"
	"os"
	"strings"
	"sync"
)

// Ledger 是 xray-clients.json 的唯一 owner。所有写操作在同一把锁内完成
// read-modify-write，避免并发 last-writer-wins。
type Ledger struct {
	mu      sync.Mutex
	layout  runtime.Layout
	file    *runtime.JSONFile[[]Client]
	aliases map[string]string
}

func NewLedger(layout runtime.Layout, notifier runtime.Notifier) *Ledger {
	return &Ledger{
		layout: layout,
		file:   runtime.NewJSONFile[[]Client](layout.XrayClientsPath(), 0600, notifier),
	}
}

// Ensure 在启动时加载并规范化台账（小写 UUID、email=UUID、去重），
// 返回后通过 Aliases() 取旧 email→UUID 的流量续接映射。
//
// 不再有 XRAY_CLIENT_IDS/legacy 配置迁移：台账文件是唯一来源。新部署
// 从空台账开始，由面板创建客户端。
func (l *Ledger) Ensure() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	clients, err := l.file.Load()
	if err != nil {
		return err
	}
	normalized, aliases, changed := normalize(clients)
	l.aliases = aliases
	if !changed {
		return nil
	}
	// 文件不存在且没有数据：不创建空台账（保持"首次运行"语义由面板创建）。
	if _, statErr := os.Stat(l.layout.XrayClientsPath()); os.IsNotExist(statErr) && len(normalized) == 0 {
		return nil
	}
	log.Printf("Normalizing xray client ledger: %d entries (lower-case UUID, email=UUID, dedupe)", len(normalized))
	return l.file.Save(normalized)
}

// Clients 返回台账快照（copy）。
func (l *Ledger) Clients() ([]Client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	clients, err := l.file.Load()
	if err != nil {
		return nil, err
	}
	out := make([]Client, len(clients))
	copy(out, clients)
	return out, nil
}

// Aliases 返回本次 Ensure 产生的旧 email→UUID 映射（无迁移时为空）。
func (l *Ledger) Aliases() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]string, len(l.aliases))
	for k, v := range l.aliases {
		out[k] = v
	}
	return out
}

func (l *Ledger) saveLocked(clients []Client) error {
	if clients == nil {
		clients = []Client{}
	}
	return l.file.Save(clients)
}

// Add 新增客户端；UUID 已存在返回 ErrDuplicateID。
func (l *Ledger) Add(entry Client) error {
	entry = normalizeClient(entry)
	if entry.ID == "" {
		return ErrInvalidClient
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	clients, err := l.file.Load()
	if err != nil {
		return err
	}
	for _, c := range clients {
		if c.ID == entry.ID {
			return ErrDuplicateID
		}
	}
	clients = append(clients, entry)
	return l.saveLocked(clients)
}

// Remove 无条件删除（回滚路径）。返回是否真的删除。
func (l *Ledger) Remove(id string) (bool, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	l.mu.Lock()
	defer l.mu.Unlock()
	clients, err := l.file.Load()
	if err != nil {
		return false, err
	}
	kept := make([]Client, 0, len(clients))
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
	return true, l.saveLocked(kept)
}

// Delete 删除指定客户端；ownerFilter 非空时只允许删除该 owner 的客户端。
// 返回被删条目供调用方回滚。
func (l *Ledger) Delete(id, ownerFilter string) (Client, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	l.mu.Lock()
	defer l.mu.Unlock()
	clients, err := l.file.Load()
	if err != nil {
		return Client{}, err
	}
	var kept []Client
	var deleted Client
	found := false
	for _, c := range clients {
		if c.ID == id {
			if ownerFilter != "" && c.Owner != ownerFilter {
				return Client{}, ErrForbidden
			}
			deleted = c
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		return Client{}, ErrNotFound
	}
	return deleted, l.saveLocked(kept)
}

// DeleteByOwner 删除某用户全部客户端，返回删除数量。
func (l *Ledger) DeleteByOwner(owner string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	clients, err := l.file.Load()
	if err != nil {
		return 0, err
	}
	var kept []Client
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
	return removed, l.saveLocked(kept)
}

func normalizeClient(c Client) Client {
	c.ID = strings.ToLower(strings.TrimSpace(c.ID))
	c.Owner = strings.TrimSpace(c.Owner)
	if c.Owner == "" {
		c.Owner = c.ID
	}
	c.Email = c.ID
	return c
}

// normalize 规范化整份台账并返回旧 email→UUID 迁移映射（歧义项剔除）。
func normalize(clients []Client) ([]Client, map[string]string, bool) {
	emailCount := make(map[string]int, len(clients))
	for _, c := range clients {
		if e := strings.TrimSpace(c.Email); e != "" {
			emailCount[strings.ToLower(e)]++
		}
	}
	aliases := make(map[string]string)
	seen := make(map[string]bool, len(clients))
	out := make([]Client, 0, len(clients))
	changed := false
	for _, c := range clients {
		id := strings.ToLower(strings.TrimSpace(c.ID))
		if id == "" || seen[id] {
			changed = true
			continue
		}
		seen[id] = true
		owner := strings.TrimSpace(c.Owner)
		if owner == "" {
			owner = id
		}
		oldEmail := strings.TrimSpace(c.Email)
		if oldEmail != "" && !strings.EqualFold(oldEmail, id) && emailCount[strings.ToLower(oldEmail)] == 1 {
			aliases[oldEmail] = id
		}
		if c.ID != id || c.Owner != owner || c.Email != id {
			changed = true
		}
		out = append(out, Client{ID: id, Owner: owner, Email: id})
	}
	return out, aliases, changed
}
