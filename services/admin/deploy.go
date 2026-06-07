package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
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
// Reads clients under xrayClientsMu, copies, then releases the lock before
// generating the config. This avoids blocking concurrent client add/delete
// operations during the (potentially slow) template processing + file write.
// The brief inconsistency window is benign: the next add/delete call triggers
// another genXrayConfig that will include the latest client set.
func genXrayConfig() error {
	tmpl, err := os.ReadFile("/app/configs/xray.json.tmpl")
	if err != nil {
		return err
	}

	xrayClientsMu.Lock()
	clients, err := readXrayClientsNoLock()
	if err != nil {
		xrayClientsMu.Unlock()
		return fmt.Errorf("read xray clients: %w", err)
	}
	// 复制切片以在锁外安全使用
	clientsCopy := make([]xrayClientEntry, len(clients))
	copy(clientsCopy, clients)
	xrayClientsMu.Unlock()

	var xhttpClients, realityClients bytes.Buffer
	enc := json.NewEncoder(&xhttpClients)
	enc.SetEscapeHTML(false)
	for _, c := range clientsCopy {
		enc.Encode(map[string]string{"id": c.ID, "email": c.Email, "flow": "", "owner": c.Owner})
		xhttpClients.Truncate(xhttpClients.Len() - 1)
		xhttpClients.WriteByte(',')
	}
	enc = json.NewEncoder(&realityClients)
	enc.SetEscapeHTML(false)
	for _, c := range clientsCopy {
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
	log.Printf("Regenerating xray config (%d clients, dns=%v)", len(clientsCopy), strings.Contains(out, `"dns"`))
	return atomicWriteFile("/app/configs/xray.json", []byte(out), 0600)
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
	return atomicWriteFile("/app/configs/nginx.conf", []byte(out), 0644)
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
	out, err := bcryptHtpasswdLine(user, pass)
	if err != nil {
		return err
	}
	return atomicWriteFile("/app/htpasswd", []byte(out), 0600)
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
	if err := atomicWriteFile("/app/data/.internal-token", []byte(tok), 0600); err != nil {
		return err
	}
	os.Setenv("INTERNAL_TOKEN", tok)
	return nil
}

// genProxyHtpasswd regenerates /app/data/proxy-htpasswd from all users' proxy passwords.
func genProxyHtpasswd() {
	doc, err := readUsersMeta()
	if err != nil {
		log.Printf("ERROR: genProxyHtpasswd: read users: %v", err)
		return
	}
	var entries []string
	for username, info := range doc.Users {
		if info.ProxyPassword == "" {
			continue
		}
		out, err := bcryptHtpasswdLine(username, info.ProxyPassword)
		if err != nil {
			log.Printf("ERROR: genProxyHtpasswd: bcrypt for %s: %v", username, err)
			continue
		}
		entries = append(entries, out)
	}
	os.MkdirAll("/app/data", 0755)
	data := []byte(strings.Join(entries, "\n") + "\n")
	if len(entries) == 0 {
		data = []byte{}
	}
	if err := atomicWriteFile("/app/data/proxy-htpasswd", data, 0600); err != nil {
		log.Printf("ERROR: genProxyHtpasswd: write file: %v", err)
	}
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
