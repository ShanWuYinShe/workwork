package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// generateConfigs creates xray config, nginx config, and htpasswd from templates.
// Called on admin startup to ensure configs exist before services come up.
func generateConfigs() error {
	if err := ensureXrayClients(); err != nil {
		return fmt.Errorf("xray clients init: %w", err)
	}
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
	genProxyHtpasswd() // ensure proxy-htpasswd file exists (even if empty)
	if err := genInternalToken(); err != nil {
		return fmt.Errorf("internal token: %w", err)
	}
	return nil
}

// genXrayConfig generates xray.json from the template + persisted client data.
// Always regenerates — client data lives in /app/data/xray-clients.json.
func genXrayConfig() error {
	tmpl, err := os.ReadFile("/app/configs/xray.json.tmpl")
	if err != nil {
		return err
	}

	clients, err := readXrayClients()
	if err != nil {
		return fmt.Errorf("read xray clients: %w", err)
	}

	var xhttpClients, realityClients bytes.Buffer
	enc := json.NewEncoder(&xhttpClients)
	enc.SetEscapeHTML(false)
	for _, c := range clients {
		enc.Encode(map[string]string{"id": c.ID, "email": c.Email, "flow": "", "owner": c.Owner})
		xhttpClients.Truncate(xhttpClients.Len() - 1)
		xhttpClients.WriteByte(',')
	}
	enc = json.NewEncoder(&realityClients)
	enc.SetEscapeHTML(false)
	for _, c := range clients {
		enc.Encode(map[string]string{"id": c.ID, "email": c.Email, "flow": "xtls-rprx-vision", "owner": c.Owner})
		realityClients.Truncate(realityClients.Len() - 1)
		realityClients.WriteByte(',')
	}

	xhttpJSON := "[]"
	if xhttpClients.Len() > 0 {
		xhttpJSON = "[" + strings.TrimSuffix(xhttpClients.String(), ",") + "]"
	}
	realityJSON := "[]"
	if realityClients.Len() > 0 {
		realityJSON = "[" + strings.TrimSuffix(realityClients.String(), ",") + "]"
	}

	out := string(tmpl)
	out = strings.ReplaceAll(out, "__XRAY_CLIENTS_JSON__", xhttpJSON)
	out = strings.ReplaceAll(out, "__XRAY_REALITY_CLIENTS_JSON__", realityJSON)
	out = replaceEnv(out, map[string]string{
		"__XRAY_REALITY_DEST__":        "XRAY_REALITY_DEST",
		"__XRAY_REALITY_SERVER_NAME__": "XRAY_REALITY_SERVER_NAME",
		"__XRAY_PRIVATE_KEY__":         "XRAY_PRIVATE_KEY",
		"__XRAY_SHORT_IDS__":           "XRAY_SHORT_IDS",
	}, map[string]string{
		"__XRAY_REALITY_DEST__": "addons.mozilla.org",
	})

	// Always write — content comparison happens inside WriteFile (atomic overwrite).
	// The file is shared with xray container via bind mount.
	existing, _ := os.ReadFile("/app/configs/xray.json")
	if string(existing) == out {
		return nil
	}
	log.Printf("Regenerating xray config (%d clients, dns=%v)", len(clients), strings.Contains(out, `"dns"`))
	return os.WriteFile("/app/configs/xray.json", []byte(out), 0600)
}

func genNginxConfig() error {
	tmpl, err := os.ReadFile("/app/configs/nginx.conf.tmpl")
	if err != nil {
		return err
	}
	out := string(tmpl)
	// XHTTP proxy auth: always use per-user proxy htpasswd
	proxyAuth := "auth_basic \"XHTTP Proxy\";\n        auth_basic_user_file /etc/nginx/htpasswd/.htpasswd-proxy;"
	out = strings.ReplaceAll(out, "__XHTTP_PROXY_AUTH__", proxyAuth)
	out = replaceEnv(out, map[string]string{
		"__NGINX_IPV6__": "NGINX_IPV6",
		"__ADMIN_PORT__": "ADMIN_PORT",
	})
	if existing, err := os.ReadFile("/app/configs/nginx.conf"); err == nil && string(existing) == out {
		return nil
	}
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
	return os.WriteFile("/app/htpasswd", bytes.TrimSpace(out), 0600)
}

func genCliproxyConfig() error {
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
				fmt.Fprintf(&keysYAML, "  - \"%s\"\n", k)
			}
		}
	}
	out := strings.ReplaceAll(string(tmpl), "__CLIPROXY_PUBLIC_API_KEYS_YAML__", keysYAML.String())
	out = strings.ReplaceAll(out, "__CLIPROXY_OPENAI_COMPAT_YAML__", "# no custom providers")
	out = strings.ReplaceAll(out, "__CLIPROXY_CLAUDE_API_KEY_YAML__", "# no custom providers")
	if existing, err := os.ReadFile("/app/configs/cliproxy.yaml"); err == nil && string(existing) == out {
		return nil
	}
	return os.WriteFile("/app/configs/cliproxy.yaml", []byte(out), 0644)
}

func seedAdminUser() error {
	adminUser := getEnv("AUTH_USER")
	if adminUser == "" {
		return nil
	}
	return updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
		if _, exists := doc.Users[adminUser]; !exists {
			doc.Users[adminUser] = UserInfo{Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
			return true, nil
		}
		return false, nil
	})
}

func genInternalToken() error {
	if os.Getenv("INTERNAL_TOKEN") != "" {
		return nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := hex.EncodeToString(b)
	if err := os.WriteFile("/app/data/.internal-token", []byte(tok), 0600); err != nil {
		return err
	}
	os.Setenv("INTERNAL_TOKEN", tok)
	return nil
}

// genProxyHtpasswd regenerates /app/data/proxy-htpasswd from all users' proxy passwords.
func genProxyHtpasswd() {
	doc, err := readUsersMeta()
	if err != nil {
		return
	}
	var entries []string
	for username, info := range doc.Users {
		if info.ProxyPassword == "" {
			continue
		}
		out, err := exec.Command("htpasswd", "-nbB", username, info.ProxyPassword).Output()
		if err != nil {
			continue
		}
		entries = append(entries, strings.TrimSpace(string(out)))
	}
	os.MkdirAll("/app/data", 0755)
	if len(entries) == 0 {
		os.WriteFile("/app/data/proxy-htpasswd", []byte{}, 0644)
		return
	}
	os.WriteFile("/app/data/proxy-htpasswd", []byte(strings.Join(entries, "\n")+"\n"), 0644)
}

func replaceEnv(s string, vars map[string]string, defaults ...map[string]string) string {
	def := map[string]string{}
	if len(defaults) > 0 {
		def = defaults[0]
	}
	for placeholder, key := range vars {
		v := getEnv(key)
		if v == "" {
			v = def[placeholder]
		}
		s = strings.ReplaceAll(s, placeholder, v)
	}
	return s
}
