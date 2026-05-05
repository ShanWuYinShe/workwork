package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// generateConfigs creates xray config, nginx config, and htpasswd from templates.
// Called on admin startup to ensure configs exist before services come up.
func generateConfigs() error {
	if err := genXrayConfig(); err != nil {
		return fmt.Errorf("xray config: %w", err)
	}
	if err := genNginxConfig(); err != nil {
		return fmt.Errorf("nginx config: %w", err)
	}
	if err := genCliproxyConfig(); err != nil {
		return fmt.Errorf("cliproxy config: %w", err)
	}
	if err := genHtpasswd(); err != nil {
		return fmt.Errorf("htpasswd: %w", err)
	}
	if err := seedAdminUser(); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	return nil
}

func genXrayConfig() error {
	// Skip if already exists and has valid JSON
	if data, err := os.ReadFile("/app/configs/xray.json"); err == nil {
		if len(data) > 10 && data[0] == '{' {
			return nil
		}
	}
	tmpl, err := os.ReadFile("/app/configs/xray.json.tmpl")
	if err != nil {
		return err
	}
	ids := getEnv("XRAY_CLIENT_IDS")
	uuids := strings.Split(ids, ",")
	var clientsJSON, realityClientsJSON bytes.Buffer
	clientsJSON.WriteByte('[')
	realityClientsJSON.WriteByte('[')
	for i, u := range uuids {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if i > 0 {
			clientsJSON.WriteByte(',')
			realityClientsJSON.WriteByte(',')
		}
		owner := getEnv("AUTH_USER")
		fmt.Fprintf(&clientsJSON, `{"id":"%s","flow":"","owner":"%s"}`, u, owner)
		fmt.Fprintf(&realityClientsJSON, `{"id":"%s","flow":"xtls-rprx-vision","owner":"%s"}`, u, owner)
	}
	clientsJSON.WriteByte(']')
	realityClientsJSON.WriteByte(']')

	out := string(tmpl)
	out = strings.ReplaceAll(out, "__XRAY_CLIENTS_JSON__", clientsJSON.String())
	out = strings.ReplaceAll(out, "__XRAY_REALITY_CLIENTS_JSON__", realityClientsJSON.String())
	out = replaceEnv(out, map[string]string{
		"__XRAY_REALITY_DEST__":        "XRAY_REALITY_DEST",
		"__XRAY_REALITY_SERVER_NAME__": "XRAY_REALITY_SERVER_NAME",
		"__XRAY_PRIVATE_KEY__":         "XRAY_PRIVATE_KEY",
		"__XRAY_SHORT_IDS__":           "XRAY_SHORT_IDS",
	})
	return os.WriteFile("/app/configs/xray.json", []byte(out), 0644)
}

func genNginxConfig() error {
	if data, err := os.ReadFile("/app/configs/nginx.conf"); err == nil {
		if len(data) > 100 {
			return nil
		}
	}
	tmpl, err := os.ReadFile("/app/configs/nginx.conf.tmpl")
	if err != nil {
		return err
	}
	out := string(tmpl)
	out = replaceEnv(out, map[string]string{
		"__NGINX_IPV6__": "NGINX_IPV6",
		"__ADMIN_PORT__": "ADMIN_PORT",
	})
	return os.WriteFile("/app/configs/nginx.conf", []byte(out), 0644)
}

func genHtpasswd() error {
	if data, err := os.ReadFile("/app/htpasswd"); err == nil {
		if len(data) > 10 {
			return nil
		}
	}
	user := getEnv("AUTH_USER")
	pass := getEnv("AUTH_PASS")
	if user == "" || pass == "" {
		return nil
	}
	out, err := exec.Command("htpasswd", "-nbB", user, pass).Output()
	if err != nil {
		return err
	}
	return os.WriteFile("/app/htpasswd", bytes.TrimSpace(out), 0644)
}

func genCliproxyConfig() error {
	if data, err := os.ReadFile("/app/configs/cliproxy.yaml"); err == nil {
		if len(data) > 10 {
			return nil
		}
	}
	tmpl, err := os.ReadFile("/app/configs/cliproxy.yaml.tmpl")
	if err != nil {
		return err
	}
	keys := getEnv("CLIPROXY_PUBLIC_API_KEYS")
	var keysYAML strings.Builder
	if keys != "" {
		for _, k := range strings.Split(keys, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				fmt.Fprintf(&keysYAML, "  - key: \"%s\"\n", k)
			}
		}
	}
	out := strings.ReplaceAll(string(tmpl), "__CLIPROXY_PUBLIC_API_KEYS_YAML__", keysYAML.String())
	out = strings.ReplaceAll(out, "__CLIPROXY_OPENAI_COMPAT_YAML__", "# no custom providers")
	return os.WriteFile("/app/configs/cliproxy.yaml", []byte(out), 0644)
}

func seedAdminUser() error {
	doc, err := readUsersMeta()
	if err != nil {
		return err
	}
	adminUser := getEnv("AUTH_USER")
	if adminUser == "" {
		return nil
	}
	if _, exists := doc.Users[adminUser]; !exists {
		doc.Users[adminUser] = UserInfo{Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
		return saveUsersMeta(doc)
	}
	return nil
}

func replaceEnv(s string, vars map[string]string) string {
	for placeholder, key := range vars {
		v := getEnv(key)
		s = strings.ReplaceAll(s, placeholder, v)
	}
	return s
}
