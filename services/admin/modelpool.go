package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type modelPoolProvider struct {
	Name    string          `json:"name"`
	BaseURL string          `json:"base_url"`
	APIKey  string          `json:"api_key"`
	Type    string          `json:"type"`   // "openai" or "anthropic"
	Models  []cliproxyModel `json:"models"` // per-model name+alias
}

func (p modelPoolProvider) getModels() []cliproxyModel {
	if len(p.Models) > 0 {
		return p.Models
	}
	return []cliproxyModel{{Name: "*", Alias: p.Name}}
}

type modelPoolConfig struct {
	Providers []modelPoolProvider `json:"providers"`
}

var modelPoolPath = "/app/data/model-pool.json"

func loadModelPool() modelPoolConfig {
	var cfg modelPoolConfig
	data, err := os.ReadFile(modelPoolPath)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

func saveModelPool(cfg modelPoolConfig) error {
	os.MkdirAll("/app/data", 0755)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(modelPoolPath, data, 0644)
}

func getUserPoolAccess(username string) bool {
	doc, err := readUsersMeta()
	if err != nil {
		return false
	}
	info, ok := doc.Users[username]
	return ok && info.PoolAccess
}

func setUserPoolAccess(username string, granted bool) error {
	doc, err := readUsersMeta()
	if err != nil {
		return err
	}
	info, ok := doc.Users[username]
	if !ok {
		info = UserInfo{Role: "viewer"}
	}
	info.PoolAccess = granted
	doc.Users[username] = info
	return saveUsersMeta(doc)
}

func poolAuthorizedUsers() []string {
	doc, err := readUsersMeta()
	if err != nil {
		return []string{}
	}
	users := make([]string, 0)
	for username, info := range doc.Users {
		if info.PoolAccess {
			users = append(users, username)
		}
	}
	return users
}

// GET/POST /api/admin/model-pool
func apiModelPool(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg := loadModelPool()
			users := poolAuthorizedUsers()
			providers := cfg.Providers
			if providers == nil {
				providers = []modelPoolProvider{}
			}
			writeJSON(w, map[string]any{
				"providers":        providers,
				"authorized_users": users,
			})
		case http.MethodPost:
			var cfg modelPoolConfig
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				writeError(w, "Invalid body: "+err.Error(), 400)
				return
			}
			if err := saveModelPool(cfg); err != nil {
				writeError(w, err.Error(), 500)
				return
			}
			regenerated := regeneratePoolConfigs(cfg.Providers, cli)
			writeJSON(w, map[string]any{
				"status":      "ok",
				"regenerated": regenerated,
			})
		}
	}
}

// POST /api/admin/model-pool/users
func apiModelPoolUsers(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Granted  bool   `json:"granted"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username and granted required", 400)
			return
		}
		if err := setUserPoolAccess(body.Username, body.Granted); err != nil {
			writeError(w, err.Error(), 500)
			return
		}

		regenerated := false
		if body.Granted {
			pool := loadModelPool()
			regenerated = regenerateUserPoolConfig(body.Username, pool.Providers, cli)
		} else {
			regenerated = regenerateUserPoolConfig(body.Username, nil, cli)
		}

		users := poolAuthorizedUsers()
		writeJSON(w, map[string]any{
			"status":           "ok",
			"regenerated":      regenerated,
			"authorized_users": users,
		})
	}
}

func regeneratePoolConfigs(poolProviders []modelPoolProvider, cli *client.Client) []string {
	var regenerated []string
	for _, username := range poolAuthorizedUsers() {
		if regenerateUserPoolConfig(username, poolProviders, cli) {
			regenerated = append(regenerated, username)
		}
	}
	return regenerated
}

func regenerateUserPoolConfig(username string, poolProviders []modelPoolProvider, cli *client.Client) bool {
	tok, err := cliproxyTokenForUser(username)
	if err != nil {
		return false
	}
	tmpl, err := os.ReadFile("/app/configs/cliproxy.yaml.tmpl")
	if err != nil {
		return false
	}

	// Read user's existing personal providers
	var userProviders []cliproxyProvider
	cfgPath := cliproxyConfigPath(tok)
	metaPath := cfgPath[:len(cfgPath)-5] + ".providers.json"
	if metaData, err := os.ReadFile(metaPath); err == nil {
		var meta struct {
			Providers []cliproxyProvider `json:"providers"`
		}
		if json.Unmarshal(metaData, &meta) == nil {
			userProviders = meta.Providers
		}
	}

	// API keys for cliproxy access
	var apiKeysYAML strings.Builder
	fmt.Fprintf(&apiKeysYAML, "  - \"%s\"\n", tok)
	pubKeys := getEnv("CLIPROXY_PUBLIC_API_KEYS")
	if pubKeys != "" {
		for _, k := range strings.Split(pubKeys, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				fmt.Fprintf(&apiKeysYAML, "  - \"%s\"\n", k)
			}
		}
	}

	// Merge: user providers + pool providers
	var allProviders []cliproxyProvider
	allProviders = append(allProviders, userProviders...)
	for _, pp := range poolProviders {
		allProviders = append(allProviders, cliproxyProvider{
			Name:    "[Pool] " + pp.Name,
			BaseURL: pp.BaseURL,
			APIKey:  pp.APIKey,
			Type:    pp.Type,
			Models:  pp.Models,
		})
	}

	// Generate separate sections by type
	var openAICompatYAML strings.Builder
	var claudeAPIKeyYAML strings.Builder
	for _, p := range allProviders {
		if p.Name == "" || p.BaseURL == "" || p.APIKey == "" {
			continue
		}
		t := p.Type
		if t == "" {
			t = "openai"
		}

		if t == "anthropic" {
			fmt.Fprintf(&claudeAPIKeyYAML, "  - api-key: \"%s\"\n", sanitizeYAML(p.APIKey))
			if p.BaseURL != "https://api.anthropic.com/v1" {
				fmt.Fprintf(&claudeAPIKeyYAML, "    base-url: \"%s\"\n", sanitizeYAML(p.BaseURL))
			}
			fmt.Fprintf(&claudeAPIKeyYAML, "    models:\n")
			for _, m := range p.getModels() {
				fmt.Fprintf(&claudeAPIKeyYAML, "      - name: \"%s\"\n", sanitizeYAML(m.Name))
				fmt.Fprintf(&claudeAPIKeyYAML, "        alias: \"%s\"\n", sanitizeYAML(m.Alias))
			}
		} else {
			fmt.Fprintf(&openAICompatYAML, "  - name: \"%s\"\n", sanitizeYAML(p.Name))
			fmt.Fprintf(&openAICompatYAML, "    base-url: \"%s\"\n", sanitizeYAML(p.BaseURL))
			fmt.Fprintf(&openAICompatYAML, "    api-key-entries:\n")
			fmt.Fprintf(&openAICompatYAML, "      - api-key: \"%s\"\n", sanitizeYAML(p.APIKey))
			fmt.Fprintf(&openAICompatYAML, "    models:\n")
			for _, m := range p.getModels() {
				fmt.Fprintf(&openAICompatYAML, "      - name: \"%s\"\n", sanitizeYAML(m.Name))
				fmt.Fprintf(&openAICompatYAML, "        alias: \"%s/*\"\n", sanitizeYAML(m.Alias))
			}
		}
	}

	out := string(tmpl)
	out = strings.ReplaceAll(out, "__CLIPROXY_PUBLIC_API_KEYS_YAML__", apiKeysYAML.String())
	out = strings.ReplaceAll(out, "__CLIPROXY_OPENAI_COMPAT_YAML__", openAICompatYAML.String())
	out = strings.ReplaceAll(out, "__CLIPROXY_CLAUDE_API_KEY_YAML__", claudeAPIKeyYAML.String())

	if idx := strings.LastIndex(cfgPath, "/"); idx >= 0 {
		dir := cfgPath[:idx]
		if err := os.MkdirAll(dir, 0755); err != nil {
			return false
		}
	}
	if err := os.WriteFile(cfgPath, []byte(out), 0644); err != nil {
		return false
	}

	// Save merged providers metadata
	metaPath = cfgPath[:len(cfgPath)-5] + ".providers.json"
	metaData, _ := json.Marshal(map[string]any{"providers": allProviders})
	if err := os.WriteFile(metaPath, metaData, 0644); err != nil {
		log.Printf("WARNING: failed to write providers metadata %s: %v", metaPath, err)
	}

	// Restart cliproxy if it's running
	restartCliproxyIfRunning(username, cli)

	return true
}

func restartCliproxyIfRunning(username string, cli *client.Client) {
	tok, err := cliproxyTokenForUser(username)
	if err != nil {
		return
	}
	conts, err := cli.ContainerList(context.Background(), container.ListOptions{
		Filters: filters.NewArgs(
			filters.KeyValuePair{Key: "label", Value: "app=cliproxy"},
			filters.KeyValuePair{Key: "label", Value: "cliproxy.token=" + tok},
		),
	})
	if err != nil || len(conts) == 0 || conts[0].State != "running" {
		return
	}
	t := 10
	cli.ContainerRestart(context.Background(), conts[0].ID, container.StopOptions{Timeout: &t})
}
