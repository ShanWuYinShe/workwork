package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"admin/handlers"
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

func writeXrayClients(clients []xrayClientEntry) error {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()

	os.MkdirAll("/app/data", 0755)
	data, err := json.MarshalIndent(clients, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(xrayClientsPath, data, 0600)
}

func addXrayClient(entry xrayClientEntry) error {
	clients, err := readXrayClients()
	if err != nil {
		return err
	}
	clients = append(clients, entry)
	return writeXrayClients(clients)
}

func deleteXrayClient(id string) error {
	clients, err := readXrayClients()
	if err != nil {
		return err
	}
	var kept []xrayClientEntry
	for _, c := range clients {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	return writeXrayClients(kept)
}

// ensureXrayClients initializes the data file on first run.
// If data file is empty, migrates from existing xray.json; falls back to env var UUIDs.
func ensureXrayClients() error {
	clients, err := readXrayClients()
	if err != nil {
		return err
	}
	if len(clients) > 0 {
		return nil // already initialized
	}

	// Try to extract clients from existing xray.json (migration from old format)
	clients = migrateXrayClientsFromConfig()
	if len(clients) == 0 {
		// Fallback: seed from env var
		ids := getEnv("XRAY_CLIENT_IDS")
		owner := getEnv("AUTH_USER")
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
	return writeXrayClients(clients)
}

// migrateXrayClientsFromConfig extracts client entries from existing xray.json.
// One-time migration: old format stored clients inside the generated config.
func migrateXrayClientsFromConfig() []xrayClientEntry {
	data, err := os.ReadFile("/app/configs/xray.json")
	if err != nil {
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

// readXrayClientMaps reads clients with locking and returns in map format for config generation.
func readXrayClientMaps() ([]map[string]string, error) {
	xrayClientsMu.Lock()
	defer xrayClientsMu.Unlock()
	return handlers.ReadXrayClients()
}
