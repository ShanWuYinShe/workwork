package main

import (
	"admin/internal/platform"
	"encoding/json"
	"errors"
	"os"
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

type xrayClientEntry struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	Email string `json:"email"`
}

var (
	xrayClientsPath = "/app/data/xray-clients.json"
	xrayClientsMu   sync.Mutex
)

func readXrayClients() ([]xrayClientEntry, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()
	return readXrayClientsNoLock()
}

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
	if err := os.MkdirAll("/app/data", 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(clients, "", "  ")
	if err != nil {
		return err
	}
	return platform.AtomicWriteFile(xrayClientsPath, data, 0600)
}

func writeXrayClients(clients []xrayClientEntry) error {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()
	return writeXrayClientsNoLock(clients)
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
func addXrayClient(entry xrayClientEntry) error {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

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

// addXrayClientIfOwnerUnique 原子地检查 owner 唯一性并添加 client。
// 如果 owner 已有 client 返回 (false, nil)，用于 viewer 的单 client 限制。
func addXrayClientIfOwnerUnique(entry xrayClientEntry) (bool, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	clients, err := readXrayClientsNoLock()
	if err != nil {
		return false, err
	}
	for _, c := range clients {
		if c.Owner == entry.Owner {
			return false, nil
		}
	}
	if clientExistsNoLock(clients, entry.ID) {
		return false, errDuplicateUUID
	}
	clients = append(clients, entry)
	return true, writeXrayClientsNoLock(clients)
}

// deleteXrayClientIfOwner 在单次锁获取内完成所有权检查和删除。
// ownerFilter 为空表示不检查所有权（admin）；非空则只允许删除属于该 owner 的 client。
func deleteXrayClientIfOwner(id string, ownerFilter string) (bool, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	clients, err := readXrayClientsNoLock()
	if err != nil {
		return false, err
	}

	var kept []xrayClientEntry
	found := false
	for _, c := range clients {
		if c.ID == id {
			if ownerFilter != "" && c.Owner != ownerFilter {
				return false, errForbidden
			}
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		return false, errNotFound
	}
	return true, writeXrayClientsNoLock(kept)
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

// ensureXrayClients initializes the data file on first run.
// 以「数据文件是否存在」判断首次运行，而非集合是否为空：管理员把客户端
// 全部删除（吊销）后文件是空集，若按空集判断会在重启时重新播下 env/
// legacy 配置里的 UUID，被吊销的代理身份悄悄恢复。
func ensureXrayClients() error {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	if _, err := os.Stat(xrayClientsPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	clients := migrateXrayClientsFromConfig()
	if len(clients) == 0 {
		ids := platform.GetEnv("XRAY_CLIENT_IDS")
		owner := platform.GetEnv("AUTH_USER")
		for _, u := range strings.Split(ids, ",") {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			clients = append(clients, xrayClientEntry{ID: u, Owner: owner, Email: owner})
		}
	}
	if len(clients) == 0 {
		return nil
	}
	return writeXrayClientsNoLock(clients)
}

func migrateXrayClientsFromConfig() []xrayClientEntry {
	var data []byte
	for _, path := range []string{xrayConfigPath, legacyXrayConfigPath} {
		var err error
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
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
			if c.ID == "" || c.ID == "test" || seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			if c.Owner == "" {
				c.Owner = c.ID
			}
			if c.Email == "" {
				c.Email = c.Owner
			}
			clients = append(clients, c)
		}
	}
	return clients
}

func readXrayClientMaps() ([]map[string]string, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()
	return ReadXrayClients()
}
