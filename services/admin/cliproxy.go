package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

type cliproxyInstance struct {
	Username   string   `json:"username"`
	Token      string   `json:"token,omitempty"`
	BaseURL    string   `json:"base_url,omitempty"`
	Status     string   `json:"status"`
	Running    bool     `json:"running"`
	Image      string   `json:"image"`
	APIKeys    []string `json:"api_keys,omitempty"`
	PoolAccess bool     `json:"pool_access"`
}

func apiCliproxyInstances(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conts, err := cli.ContainerList(r.Context(), container.ListOptions{
			All: true,
			Filters: filters.NewArgs(filters.KeyValuePair{
				Key: "label", Value: "app=cliproxy",
			}),
		})
		if err != nil {
			writeError(w, "Docker API error: "+err.Error(), 500)
			return
		}
		// index containers by token label
		byToken := map[string]string{} // token -> state
		for _, c := range conts {
			tok := c.Labels["cliproxy.token"]
			if tok == "" {
				tok = "public"
			}
			byToken[tok] = c.State
		}

		doc, err := readUsersMeta()
		if err != nil {
			writeError(w, "Failed to read users: "+err.Error(), 500)
			return
		}
		// Only show users that exist in htpasswd
		htUsers := readHtpasswdUsers()
		htSet := map[string]bool{}
		for _, u := range htUsers {
			htSet[u] = true
		}
		var instances []cliproxyInstance
		// Per-user (only actual htpasswd users, skip admins)
		for username, info := range doc.Users {
			if !htSet[username] || info.Role == "admin" {
				continue
			}
			tok := info.CliproxyToken
			if tok == "" {
				tok, _ = getOrCreateCliproxyToken(username)
			}
			state := byToken[tok]
			if state == "" {
				state = "stopped"
			}
			domain := getEnv("XHTTP_DOMAIN")
			if domain == "" {
				domain = "yinghuoren.com"
			}
			instances = append(instances, cliproxyInstance{
				Username:   username,
				Token:      tok,
				BaseURL:    "https://" + domain + "/api/cliproxy/user/" + tok,
				Status:     state,
				Running:    state == "running",
				PoolAccess: info.PoolAccess,
			})
		}
		writeJSON(w, map[string]any{"instances": instances})
	}
}

func apiCliproxyStart(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username required", 400)
			return
		}
		if body.Username == "public" {
			writeError(w, "Use docker compose to manage public instance", 400)
			return
		}
		if err := startCliproxy(r.Context(), cli, body.Username); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiCliproxyStop(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username required", 400)
			return
		}
		if body.Username == "public" {
			writeError(w, "Use docker compose to manage public instance", 400)
			return
		}
		if err := stopCliproxy(r.Context(), cli, body.Username); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiCliproxyGetConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username required", 400)
			return
		}
		tok, err := cliproxyTokenForUser(body.Username)
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		cfgPath := cliproxyConfigPath(tok)
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			// Return empty config for new users
			writeJSON(w, map[string]any{
				"config":   "",
				"api_keys": []string{},
			})
			return
		}
		// Read providers from companion JSON file
		providers := []cliproxyProvider{}
		metaPath := cfgPath[:len(cfgPath)-5] + ".providers.json" // config.yaml → .providers.json
		if metaData, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Providers []cliproxyProvider `json:"providers"`
			}
			if json.Unmarshal(metaData, &meta) == nil {
				for i := range meta.Providers {
					meta.Providers[i].APIKey = maskKey(meta.Providers[i].APIKey)
				}
				providers = meta.Providers
			}
		}
		writeJSON(w, map[string]any{
			"config":    string(data),
			"providers": providers,
		})
	}
}

type cliproxyModel struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
}

type cliproxyProvider struct {
	Name    string          `json:"name"`
	BaseURL string          `json:"base_url"`
	APIKey  string          `json:"api_key"`
	Type    string          `json:"type"`   // "openai" or "anthropic"
	Models  []cliproxyModel `json:"models"` // per-model name+alias
}

func (p cliproxyProvider) getModels() []cliproxyModel {
	if len(p.Models) > 0 {
		return p.Models
	}
	return []cliproxyModel{{Name: "*", Alias: p.Name}}
}

func apiCliproxySetConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username  string             `json:"username"`
			Providers []cliproxyProvider `json:"providers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username and providers required", 400)
			return
		}

		tok, err := cliproxyTokenForUser(body.Username)
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		tmpl, err := os.ReadFile("/app/configs/cliproxy.yaml.tmpl")
		if err != nil {
			writeError(w, "Template not found: "+err.Error(), 500)
			return
		}

		// User API keys (for connecting TO cliproxy, NOT upstream provider keys)
		var apiKeysYAML strings.Builder
		userKeys := getEnv("CLIPROXY_PUBLIC_API_KEYS")
		if userKeys != "" {
			for _, k := range strings.Split(userKeys, ",") {
				k = strings.TrimSpace(k)
				if k != "" {
					fmt.Fprintf(&apiKeysYAML, "  - \"%s\"\n", k)
				}
			}
		}
		// Also add per-user dedicated key
		fmt.Fprintf(&apiKeysYAML, "  - \"%s\"\n", tok)

		// Merge: personal providers + pool providers
		var allProviders []cliproxyProvider
		allProviders = append(allProviders, body.Providers...)
		if getUserPoolAccess(body.Username) {
			pool := loadModelPool()
			for _, pp := range pool.Providers {
				allProviders = append(allProviders, cliproxyProvider{
					Name:    "[Pool] " + pp.Name,
					BaseURL: pp.BaseURL,
					APIKey:  pp.APIKey,
					Type:    pp.Type,
					Models:  pp.Models,
				})
			}
		}

		// Generate separate YAML per provider type
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
				// claude-api-key section format
				fmt.Fprintf(&claudeAPIKeyYAML, "  - api-key: \"%s\"\n", p.APIKey)
				if p.BaseURL != "https://api.anthropic.com/v1" {
					fmt.Fprintf(&claudeAPIKeyYAML, "    base-url: \"%s\"\n", p.BaseURL)
				}
				fmt.Fprintf(&claudeAPIKeyYAML, "    models:\n")
				for _, m := range p.getModels() {
					fmt.Fprintf(&claudeAPIKeyYAML, "      - name: \"%s\"\n", m.Name)
					fmt.Fprintf(&claudeAPIKeyYAML, "        alias: \"%s\"\n", m.Alias)
				}
			} else {
				// openai-compatibility section format
				fmt.Fprintf(&openAICompatYAML, "  - name: \"%s\"\n", p.Name)
				fmt.Fprintf(&openAICompatYAML, "    base-url: \"%s\"\n", p.BaseURL)
				fmt.Fprintf(&openAICompatYAML, "    api-key-entries:\n")
				fmt.Fprintf(&openAICompatYAML, "      - api-key: \"%s\"\n", p.APIKey)
				fmt.Fprintf(&openAICompatYAML, "    models:\n")
				for _, m := range p.getModels() {
					fmt.Fprintf(&openAICompatYAML, "      - name: \"%s\"\n", m.Name)
					fmt.Fprintf(&openAICompatYAML, "        alias: \"%s/*\"\n", m.Alias)
				}
			}
		}

		out := string(tmpl)
		out = strings.ReplaceAll(out, "__CLIPROXY_PUBLIC_API_KEYS_YAML__", apiKeysYAML.String())
		out = strings.ReplaceAll(out, "__CLIPROXY_OPENAI_COMPAT_YAML__", openAICompatYAML.String())
		out = strings.ReplaceAll(out, "__CLIPROXY_CLAUDE_API_KEY_YAML__", claudeAPIKeyYAML.String())

		cfgPath := cliproxyConfigPath(tok)
		dir := cfgPath[:strings.LastIndex(cfgPath, "/")]
		if err := os.MkdirAll(dir, 0755); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		if err := os.WriteFile(cfgPath, []byte(out), 0644); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		// Save merged providers metadata alongside config
		metaPath := cfgPath[:len(cfgPath)-5] + ".providers.json"
		metaData, _ := json.Marshal(map[string]any{"providers": allProviders})
		os.WriteFile(metaPath, metaData, 0644)

		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiCliproxyCodexLogin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username required", 400)
			return
		}
		tok, err := cliproxyTokenForUser(body.Username)
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		conts, err := cli.ContainerList(r.Context(), container.ListOptions{
			Filters: filters.NewArgs(
				filters.KeyValuePair{Key: "label", Value: "app=cliproxy"},
				filters.KeyValuePair{Key: "label", Value: "cliproxy.token=" + tok},
			),
		})
		if err != nil || len(conts) == 0 || conts[0].State != "running" {
			writeError(w, "Container not running, start cliproxy first", 400)
			return
		}

		// Run codex device login in the background, capture first output
		execConfig := container.ExecOptions{
			Cmd:          []string{"cli-proxy-api", "-codex-device-login", "--no-browser"},
			AttachStdout: true,
			AttachStderr: true,
		}
		execResp, err := cli.ContainerExecCreate(r.Context(), conts[0].ID, execConfig)
		if err != nil {
			writeError(w, "Exec failed: "+err.Error(), 500)
			return
		}
		attachResp, err := cli.ContainerExecAttach(r.Context(), execResp.ID, container.ExecStartOptions{})
		if err != nil {
			writeError(w, "Attach failed: "+err.Error(), 500)
			return
		}
		defer attachResp.Close()

		var outBuf strings.Builder
		// Read first ~500 bytes and return immediately
		buf := make([]byte, 512)
		n, _ := attachResp.Reader.Read(buf)
		outBuf.Write(buf[:n])

		writeJSON(w, map[string]any{
			"status": "started",
			"output": outBuf.String(),
		})
	}
}

func apiCliproxyUploadOAuth(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.FormValue("username")
		if username == "" {
			writeError(w, "Username required", 400)
			return
		}
		tok, err := cliproxyTokenForUser(username)
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, "File required: "+err.Error(), 400)
			return
		}
		defer file.Close()

		name := filepath.Base(header.Filename)
		if !strings.HasSuffix(name, ".json") {
			writeError(w, "Only .json OAuth files supported", 400)
			return
		}

		authDir := fmt.Sprintf("/app/cliproxy-data/%s", tok)
		if err := os.MkdirAll(authDir, 0755); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		out, err := os.Create(fmt.Sprintf("%s/%s", authDir, name))
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		defer out.Close()
		if _, err := io.Copy(out, file); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "file": name})
	}
}

func apiCliproxyPublicConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			data, err := os.ReadFile("/app/configs/cliproxy.yaml")
			if err != nil {
				writeJSON(w, map[string]any{"config": "", "api_keys": []string{}})
				return
			}
			writeJSON(w, map[string]any{"config": string(data)})
		case http.MethodPost:
			var body struct {
				APIKeys []string `json:"api_keys"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, "Invalid body", 400)
				return
			}
			tmpl, _ := os.ReadFile("/app/configs/cliproxy.yaml.tmpl")
			var keysYAML strings.Builder
			for _, k := range body.APIKeys {
				k = strings.TrimSpace(k)
				if k != "" {
					fmt.Fprintf(&keysYAML, "  - \"%s\"\n", k)
				}
			}
			out := strings.ReplaceAll(string(tmpl), "__CLIPROXY_PUBLIC_API_KEYS_YAML__", keysYAML.String())
			out = strings.ReplaceAll(out, "__CLIPROXY_OPENAI_COMPAT_YAML__", "# no custom providers")
			out = strings.ReplaceAll(out, "__CLIPROXY_CLAUDE_API_KEY_YAML__", "# no custom providers")
			os.WriteFile("/app/configs/cliproxy.yaml", []byte(out), 0644)
			writeJSON(w, map[string]string{"status": "ok"})
		}
	}
}

func startCliproxy(ctx context.Context, cli *client.Client, username string) error {
	tok, err := getOrCreateCliproxyToken(username)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}

	conts, err := cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.KeyValuePair{Key: "label", Value: "app=cliproxy"},
			filters.KeyValuePair{Key: "label", Value: "cliproxy.token=" + tok},
		),
	})
	if err != nil {
		return err
	}
	if len(conts) > 0 {
		if conts[0].State == "running" {
			return nil
		}
		return cli.ContainerStart(ctx, conts[0].ID, container.StartOptions{})
	}

	cfgPath := cliproxyConfigPath(tok)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return fmt.Errorf("config not found, configure API keys first")
	}

	projectDir := getEnv("PROJECT_DIR")
	if projectDir == "" {
		projectDir = "/home/workuser/service"
	}

	containerName := fmt.Sprintf("cliproxy-%s-%s", username, tok[:8])
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: fmt.Sprintf("%s/cliproxy-data/%s/config.yaml", projectDir, tok),
				Target: "/CLIProxyAPI/config.yaml",
			},
			{
				Type:   mount.TypeBind,
				Source: fmt.Sprintf("%s/cliproxy-data/%s", projectDir, tok),
				Target: "/root/.cli-proxy-api",
			},
		},
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}
	netConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"workwork_internal_docker": {},
		},
	}

	cfg := &container.Config{
		Image: "workwork-cliproxy-public:latest",
		ExposedPorts: nat.PortSet{
			"8317/tcp": struct{}{},
		},
		Labels: map[string]string{
			"app":                        "cliproxy",
			"cliproxy.token":             tok,
			"com.docker.compose.project": "",
			"com.docker.compose.service": "",
			"com.docker.compose.version": "",
		},
	}

	body, err := cli.ContainerCreate(ctx, cfg, hostConfig, netConfig, nil, containerName)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	return cli.ContainerStart(ctx, body.ID, container.StartOptions{})
}

func stopCliproxy(ctx context.Context, cli *client.Client, username string) error {
	tok, err := getOrCreateCliproxyToken(username)
	if err != nil {
		return err
	}
	conts, err := cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(
			filters.KeyValuePair{Key: "label", Value: "app=cliproxy"},
			filters.KeyValuePair{Key: "label", Value: "cliproxy.token=" + tok},
		),
	})
	if err != nil || len(conts) == 0 {
		return fmt.Errorf("container not found")
	}
	timeout := 10
	return cli.ContainerStop(ctx, conts[0].ID, container.StopOptions{Timeout: &timeout})
}

func cliproxyConfigPath(token string) string {
	return fmt.Sprintf("/app/cliproxy-data/%s/config.yaml", token)
}

func cliproxyTokenForUser(username string) (string, error) {
	return getOrCreateCliproxyToken(username)
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}
