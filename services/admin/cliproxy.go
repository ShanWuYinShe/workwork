package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

type cliproxyInstance struct {
	Username string   `json:"username"`
	Token    string   `json:"token,omitempty"`
	BaseURL  string   `json:"base_url,omitempty"`
	Status   string   `json:"status"`
	Running  bool     `json:"running"`
	Image    string   `json:"image"`
	APIKeys  []string `json:"api_keys,omitempty"`
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
		var instances []cliproxyInstance
		// Per-user (public instance not exposed)
		for username, info := range doc.Users {
			tok := info.CliproxyToken
			if tok == "" {
				tok, _ = getOrCreateCliproxyToken(username)
			}
			state := byToken[tok]
			if state == "" {
				state = "stopped"
			}
			instances = append(instances, cliproxyInstance{
				Username: username,
				Token:    tok,
				BaseURL:  "https://yinghuoren.com/api/cliproxy/user/" + tok,
				Status:   state,
				Running:  state == "running",
			})
		}
		writeJSON(w, map[string]any{"instances": instances})
	}
}

func apiCliproxyStart(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username string `json:"username"` }
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
		var body struct{ Username string `json:"username"` }
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
		var body struct{ Username string `json:"username"` }
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
		var cfg map[string]any
		json.Unmarshal(data, &cfg)
		keys := []string{}
		if apiKeys, ok := cfg["api-keys"].([]any); ok {
			for _, k := range apiKeys {
				if km, ok := k.(map[string]any); ok {
					if key, ok := km["key"].(string); ok {
						keys = append(keys, maskKey(key))
					}
				}
			}
		}
		writeJSON(w, map[string]any{
			"config":   string(data),
			"api_keys": keys,
		})
	}
}

func apiCliproxySetConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string   `json:"username"`
			APIKeys  []string `json:"api_keys"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username and api_keys required", 400)
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
		var keysYAML strings.Builder
		// Public keys (injected automatically, not visible to user)
		pubKeys := getEnv("CLIPROXY_PUBLIC_API_KEYS")
		if pubKeys != "" {
			for _, k := range strings.Split(pubKeys, ",") {
				k = strings.TrimSpace(k)
				if k != "" {
					fmt.Fprintf(&keysYAML, "  - key: \"%s\"\n", k)
				}
			}
		}
		// User's private keys
		for _, k := range body.APIKeys {
			fmt.Fprintf(&keysYAML, "  - key: \"%s\"\n", k)
		}
		out := strings.ReplaceAll(string(tmpl), "__CLIPROXY_API_KEYS_YAML__", keysYAML.String())

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
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiCliproxyCodexLogin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username string `json:"username"` }
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

	containerName := fmt.Sprintf("workwork-cliproxy-%s", tok)
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: cfgPath,
				Target: "/CLIProxyAPI/config.yaml",
			},
			{
				Type:   mount.TypeBind,
				Source: fmt.Sprintf("/app/cliproxy-data/%s", tok),
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
			"app":            "cliproxy",
			"cliproxy.token": tok,
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
