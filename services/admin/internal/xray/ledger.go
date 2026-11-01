package xray

import (
	"admin/internal/runtime"
	"fmt"
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

// Ensure 在启动时加载并规范化台账（小写 UUID、生成/规范化客户端用户名、
// 去重），返回后通过 Aliases() 取旧 email→新 email 的流量续接映射。
//
// 迁移规则：旧台账 email=UUID 的条目使用 owner 作为用户名；同一 owner 的
// 后续客户端生成 owner-2、owner-3；旧自定义 email 原样保留为用户名。
// 不再有 XRAY_CLIENT_IDS/legacy 配置迁移：台账文件是唯一来源。
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
	log.Printf("Normalizing xray client ledger: %d entries (lower-case UUID, name/email migration, dedupe)", len(normalized))
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

// Aliases 返回本次 Ensure 产生的旧 email→新 email 映射（无迁移时为空）。
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

// Add 新增客户端。Name 为空时按 owner 自动生成唯一用户名；显式 Name 必须
// 合法且未被占用。UUID 已存在返回 ErrDuplicateID。
func (l *Ledger) Add(entry Client) (Client, error) {
	entry = normalizeIdentity(entry)
	if entry.ID == "" {
		return Client{}, ErrInvalidClient
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	clients, err := l.file.Load()
	if err != nil {
		return Client{}, err
	}
	used := usedNames(clients)
	if entry.Name == "" {
		entry.Name = generateName(entry.Owner, used)
	} else {
		if !ValidClientName(entry.Name) {
			return Client{}, ErrInvalidName
		}
		if used[entry.Name] {
			return Client{}, ErrDuplicateName
		}
		used[entry.Name] = true
	}
	for _, c := range clients {
		if strings.EqualFold(c.ID, entry.ID) {
			return Client{}, ErrDuplicateID
		}
	}
	clients = append(clients, entry)
	return entry, l.saveLocked(clients)
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

// normalizeIdentity 只做字段级规范化，不处理用户名唯一性（需要整份台账）。
func normalizeIdentity(c Client) Client {
	c.ID = strings.ToLower(strings.TrimSpace(c.ID))
	c.Owner = strings.TrimSpace(c.Owner)
	if c.Owner == "" {
		c.Owner = c.ID
	}
	c.Name = normalizeClientName(c.Name)
	c.LegacyEmail = ""
	return c
}

// usedNames 返回已占用的客户端用户名（小写）。
func usedNames(clients []Client) map[string]bool {
	used := make(map[string]bool, len(clients))
	for _, c := range clients {
		if name := normalizeClientName(c.Name); name != "" {
			used[name] = true
		}
	}
	return used
}

// generateName 基于 owner 生成唯一用户名：owner、owner-2、owner-3……
func generateName(owner string, used map[string]bool) string {
	base := sanitizeNameBase(owner)
	for i := 1; i <= 10000; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		if len(candidate) > 64 {
			candidate = candidate[:64]
		}
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
	// 理论不可达；保留可诊断的兜底而不是返回空 email。
	candidate := "client-" + base
	if len(candidate) > 64 {
		candidate = candidate[:64]
	}
	return candidate
}

func sanitizeNameBase(owner string) string {
	var b strings.Builder
	for _, r := range normalizeClientName(owner) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	base := strings.Trim(b.String(), "._-")
	if base == "" {
		base = "client"
	}
	// 给 -2/-3 后缀留空间。
	if len(base) > 60 {
		base = base[:60]
	}
	return base
}

// normalize 规范化整份台账并返回旧 email→新 email 迁移映射（歧义项剔除）。
func normalize(clients []Client) ([]Client, map[string]string, bool) {
	emailCount := make(map[string]int, len(clients))
	for _, c := range clients {
		if e := normalizeClientName(c.LegacyEmail); e != "" {
			emailCount[e]++
		}
	}
	aliases := make(map[string]string)
	seen := make(map[string]bool, len(clients))
	used := make(map[string]bool, len(clients))
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
		oldEmail := normalizeClientName(c.LegacyEmail)
		name := normalizeClientName(c.Name)
		// 旧自定义 email 原样延续为客户端用户名；email==UUID 的旧默认值
		// 不延续（改用 owner 生成），这正是本次迁移的目的。
		if name == "" && oldEmail != "" && !strings.EqualFold(oldEmail, id) {
			name = oldEmail
		}
		if !ValidClientName(name) || used[name] {
			name = generateName(owner, used)
		} else {
			used[name] = true
		}
		if oldEmail != "" && oldEmail != name && emailCount[oldEmail] == 1 {
			aliases[oldEmail] = name
		}
		if c.ID != id || c.Owner != owner || c.Name != name || c.LegacyEmail != "" {
			changed = true
		}
		out = append(out, Client{ID: id, Owner: owner, Name: name})
	}
	return out, aliases, changed
}
