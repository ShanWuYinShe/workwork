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
	"syscall"
	"time"
)

// generateConfigs creates runtime configs and auth files from templates.
// Called on admin startup before xray/nginx are allowed to start.
func generateConfigs() error {
	if err := ensureRuntimeDirs(); err != nil {
		return fmt.Errorf("runtime dirs: %w", err)
	}
	if err := ensureXrayClients(); err != nil {
		return fmt.Errorf("xray clients init: %w", err)
	}
	if err := ensureMediaCenterConfig(); err != nil {
		return fmt.Errorf("media center config init: %w", err)
	}
	if err := genXrayConfig(); err != nil {
		return fmt.Errorf("xray config: %w", err)
	}
	if err := genMediaCenterCategories(); err != nil {
		return fmt.Errorf("media center categories: %w", err)
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
	if err := genProxyHtpasswd(); err != nil {
		return fmt.Errorf("xhttp htpasswd: %w", err)
	}
	if err := genInternalToken(); err != nil {
		return fmt.Errorf("internal token: %w", err)
	}
	return nil
}

func runtimeFilesReady() error {
	required := []string{
		xrayConfigPath,
		nginxConfigPath,
		mediaCenterCategoriesPath,
		htpasswdFile,
		proxyHtpasswdPath,
		"/app/data/.internal-token",
	}
	for _, path := range required {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if path != proxyHtpasswdPath && info.Size() == 0 {
			return fmt.Errorf("%s is empty", path)
		}
	}
	return nil
}

func ensureRuntimeDirs() error {
	for _, dir := range []string{
		"/app/data/generated/xray",
		"/app/data/generated/nginx",
		"/app/data/generated/media_center",
		"/app/data/media-center",
		nginxAuthDir,
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
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
	tmpl, err := os.ReadFile(xrayConfigTemplatePath)
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
	if err := os.MkdirAll("/app/data/generated/xray", 0755); err != nil {
		return err
	}
	existing, _ := os.ReadFile(xrayConfigPath)
	if string(existing) == out {
		return ensurePerm(xrayConfigPath, 0644)
	}
	log.Printf("Regenerating xray config (%d clients, dns=%v)", len(clientsCopy), strings.Contains(out, `"dns"`))
	return atomicWriteFile(xrayConfigPath, []byte(out), 0644)
}

func genNginxConfig() error {
	tmpl, err := os.ReadFile(nginxConfigTemplatePath)
	if err != nil {
		return err
	}
	out := string(tmpl)
	mediaCfg, err := readMediaCenterConfig()
	if err != nil {
		return err
	}
	out = renderNginxConfigWithMediaCenter(out, mediaCfg)
	// XHTTP proxy auth: always use per-user proxy htpasswd
	proxyAuth := "auth_basic \"XHTTP Proxy\";\n        auth_basic_user_file /etc/nginx/htpasswd/.htpasswd-proxy;"
	out = strings.ReplaceAll(out, "__XHTTP_PROXY_AUTH__", proxyAuth)
	out = replaceEnv(out, map[string]string{
		"__NGINX_IPV6__": "NGINX_IPV6",
		"__ADMIN_PORT__": "ADMIN_PORT",
	}, map[string]string{
		"__ADMIN_PORT__": "8318",
	})
	if err := os.MkdirAll("/app/data/generated/nginx", 0755); err != nil {
		return err
	}
	if existing, err := os.ReadFile(nginxConfigPath); err == nil && string(existing) == out {
		return ensurePerm(nginxConfigPath, 0644)
	}
	return atomicWriteFile(nginxConfigPath, []byte(out), 0644)
}

func genHtpasswd() error {
	if err := os.MkdirAll(nginxAuthDir, 0755); err != nil {
		return err
	}
	if data, err := os.ReadFile(htpasswdFile); err == nil {
		if len(data) > 10 {
			return nil
		}
	}
	if data, err := os.ReadFile(legacyHtpasswdPath); err == nil && len(data) > 10 {
		return atomicWriteFile(htpasswdFile, data, 0644)
	}
	user := getEnv("AUTH_USER")
	pass := getEnv("AUTH_PASS")
	if user == "" || pass == "" {
		return fmt.Errorf("AUTH_USER and AUTH_PASS are required when no existing htpasswd is available")
	}
	out, err := bcryptHtpasswdLine(user, pass)
	if err != nil {
		return err
	}
	return atomicWriteFile(htpasswdFile, []byte(out), 0644)
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
	if tok := getEnv("INTERNAL_TOKEN"); tok != "" {
		if existing, err := os.ReadFile("/app/data/.internal-token"); err == nil && strings.TrimSpace(string(existing)) == tok {
			return ensurePerm("/app/data/.internal-token", 0644)
		}
		return atomicWriteFile("/app/data/.internal-token", []byte(tok), 0644)
	}
	if existing, err := os.ReadFile("/app/data/.internal-token"); err == nil {
		tok := strings.TrimSpace(string(existing))
		if tok != "" {
			os.Setenv("INTERNAL_TOKEN", tok)
			return nil
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := hex.EncodeToString(b)
	if err := atomicWriteFile("/app/data/.internal-token", []byte(tok), 0644); err != nil {
		return err
	}
	os.Setenv("INTERNAL_TOKEN", tok)
	return nil
}

// genProxyHtpasswd regenerates nginx's per-user XHTTP proxy htpasswd file.
func genProxyHtpasswd() error {
	doc, err := readUsersMeta()
	if err != nil {
		return fmt.Errorf("read users: %w", err)
	}
	var entries []string
	for username, info := range doc.Users {
		if info.ProxyPassword == "" {
			continue
		}
		out, err := bcryptHtpasswdLine(username, info.ProxyPassword)
		if err != nil {
			return fmt.Errorf("bcrypt for %s: %w", username, err)
		}
		entries = append(entries, out)
	}
	if err := os.MkdirAll(nginxAuthDir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data := []byte(strings.Join(entries, "\n") + "\n")
	if len(entries) == 0 {
		data = []byte{}
	}
	if err := atomicWriteFile(proxyHtpasswdPath, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// ensurePerm 修正文件权限。内容不变但权限过期时（如从 0600 升级到 0644），
// atomicWriteFile 的内容比较会跳过写入，导致权限不更新。此函数补上 chmod。
func ensurePerm(path string, want os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != want.Perm() {
		log.Printf("Fixing permissions: %s %o -> %o", path, info.Mode().Perm(), want.Perm())
		// os.Chmod 受 umask 影响，先用 syscall 确保精确权限
		return syscall.Chmod(path, uint32(want.Perm()))
	}
	return nil
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
