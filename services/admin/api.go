package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"admin/handlers"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// maxDownloadSize caps the size of a single binary download (Xray, Claude Code).
// Prevents a malicious or compromised CDN from filling the container's disk.
const maxDownloadSize = 500 << 20 // 500 MiB

type jsonResp struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonResp{Success: true, Data: data})
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(jsonResp{Success: false, Error: msg})
}

// writeInternalError returns a generic 500 to the client while logging the
// full error server-side. Use this for any error whose underlying message
// could leak internal state (file paths, docker container IDs, CF API
// responses, JSON struct field names, etc.).
func writeInternalError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeError(w, "Internal error", http.StatusInternalServerError)
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// proxyAuthForUser returns "username:password" for the xhttp proxy Authorization header.
// Creates a per-user proxy password on first call.
func proxyAuthForUser(username string) string {
	if username == "" || username == "internal" {
		return ""
	}
	// Admin users don't need proxy passwords
	doc, _ := readUsersMeta()
	if info, ok := doc.Users[username]; ok && info.Role == "admin" {
		return ""
	}
	pass, err := getOrCreateProxyPassword(username)
	if err != nil || pass == "" {
		return ""
	}
	return username + ":" + pass
}

func apiDashboard(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

		// For non-admin users, return personalized dashboard
		if userInfo != nil && userInfo.Role != "admin" {
			allClients, err := readXrayClientMaps()
			var myClients []map[string]string
			if err == nil {
				for _, c := range allClients {
					if c["owner"] == username {
						myClients = append(myClients, c)
					}
				}
			}
			genJSON := handlers.GenerateClientConfig(myClients, handlers.ClientConfig{
				RealityDomain:     getEnv("XRAY_REALITY_DOMAIN"),
				RealityServerName: getEnv("XRAY_REALITY_SERVER_NAME"),
				RealityPublicKey:  getEnv("XRAY_PUBLIC_KEY"),
				RealityShortID:    getEnv("XRAY_SHORT_IDS"),
				XHTTPDomain:       getEnv("XHTTP_DOMAIN"),
				XHTTPProxyAuth:    proxyAuthForUser(username),
			})
			writeJSON(w, map[string]any{
				"role":          userInfo.Role,
				"username":      username,
				"myClients":     myClients,
				"generatedJSON": genJSON,
			})
			return
		}

		// Admin: full system dashboard
		info, err := cli.Info(r.Context())
		stats := map[string]string{
			"services": "0", "running": "0", "dockerVersion": "-",
			"cpu": "0", "memory": "-", "os": "-", "arch": "-",
		}
		if err == nil {
			stats = map[string]string{
				"services":      fmt.Sprintf("%d", info.Containers),
				"running":       fmt.Sprintf("%d", info.ContainersRunning),
				"dockerVersion": info.ServerVersion,
				"cpu":           fmt.Sprintf("%d", info.NCPU),
				"memory":        fmt.Sprintf("%.1f GB", float64(info.MemTotal)/1024/1024/1024),
				"os":            info.OSType,
				"arch":          info.Architecture,
			}
		}
		svcs := listServicesJSON(r.Context(), cli)
		writeJSON(w, map[string]any{"stats": stats, "services": svcs, "role": "admin"})
	}
}

func apiServices(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svcs := listServicesJSON(r.Context(), cli)
		writeJSON(w, map[string]any{"services": svcs})
	}
}

func apiServiceAction(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		action := r.PathValue("action")

		// 校验服务名格式，仅允许小写字母、数字、连字符
		if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(name) {
			writeError(w, "Invalid service name", 400)
			return
		}
		containers, err := cli.ContainerList(r.Context(), container.ListOptions{
			All: true,
			Filters: filters.NewArgs(filters.KeyValuePair{
				Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", name),
			}),
		})
		if err != nil || len(containers) == 0 {
			writeError(w, "Service not found", 404)
			return
		}
		switch action {
		case "start":
			err = cli.ContainerStart(r.Context(), containers[0].ID, container.StartOptions{})
		case "stop":
			t := 10
			err = cli.ContainerStop(r.Context(), containers[0].ID, container.StopOptions{Timeout: &t})
		case "restart":
			t := 10
			err = cli.ContainerRestart(r.Context(), containers[0].ID, container.StopOptions{Timeout: &t})
			if err == nil && name == "xray" {
				restoreRotatingIP(r.Context(), cli, containers[0].ID)
			}
		default:
			writeError(w, "Unknown action", 400)
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}
		svcs := listServicesJSON(r.Context(), cli)
		writeJSON(w, map[string]any{"services": svcs})
	}
}

func apiConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{
			"xrayInbounds": 0, "xrayClients": 0,
			"configStatus": "-",
		}
		raw, err := os.ReadFile("/app/configs/xray.json")
		if err == nil {
			var cfg struct {
				Inbounds []struct {
					Tag      string `json:"tag"`
					Settings struct {
						Clients []map[string]any `json:"clients"`
					} `json:"settings"`
				} `json:"inbounds"`
			}
			if json.Unmarshal(raw, &cfg) == nil {
				for _, ib := range cfg.Inbounds {
					if ib.Tag == "reality-in" {
						data["xrayClients"] = len(ib.Settings.Clients)
					}
				}
				data["xrayInbounds"] = len(cfg.Inbounds)
			}
			data["configStatus"] = "configured"
		}
		writeJSON(w, data)
	}
}

type svcJSON struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Image string `json:"image"`
	Ports string `json:"ports"`
}

func listServicesJSON(ctx context.Context, cli *client.Client) []svcJSON {
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(filters.KeyValuePair{
			Key: "label", Value: "com.docker.compose.project",
		}),
	})
	if err != nil {
		return nil
	}
	project := ""
	var services []svcJSON
	for _, c := range containers {
		if project == "" && c.Labels["com.docker.compose.project"] != "" {
			project = c.Labels["com.docker.compose.project"]
		}
		if c.Labels["com.docker.compose.project"] != project {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		if svc := c.Labels["com.docker.compose.service"]; svc != "" {
			name = svc
		}
		var ports []string
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				ports = append(ports, fmt.Sprintf("%d→%d", p.PublicPort, p.PrivatePort))
			} else {
				ports = append(ports, fmt.Sprintf("%d/%s", p.PrivatePort, p.Type))
			}
		}
		services = append(services, svcJSON{
			Name: name, State: c.State, Image: c.Image,
			Ports: strings.Join(ports, ", "),
		})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services
}

// -- User management APIs --

func apiUsers(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)
		allClients, err := readXrayClientMaps()
		if err != nil {
			log.Printf("Failed to read clients: : %v", err)
			writeInternalError(w, err)
			return
		}

		// Filter by owner: admin sees all, viewer sees only own
		var filtered []map[string]string
		if userInfo != nil && userInfo.Role == "admin" {
			filtered = allClients
		} else {
			for _, c := range allClients {
				if c["owner"] == username {
					filtered = append(filtered, c)
				}
			}
		}

		// Proxy auth: use first client's owner, not the viewing user
		proxyUser := username
		if len(filtered) > 0 && filtered[0]["owner"] != "" {
			proxyUser = filtered[0]["owner"]
		}
		genJSON := handlers.GenerateClientConfig(filtered, handlers.ClientConfig{
			RealityDomain:     getEnv("XRAY_REALITY_DOMAIN"),
			RealityServerName: getEnv("XRAY_REALITY_SERVER_NAME"),
			RealityPublicKey:  getEnv("XRAY_PUBLIC_KEY"),
			RealityShortID:    getEnv("XRAY_SHORT_IDS"),
			XHTTPDomain:       getEnv("XHTTP_DOMAIN"),
			XHTTPProxyAuth:    proxyAuthForUser(proxyUser),
		})

		// Build adminUsers with role info
		doc, _ := readUsersMeta()
		type adminUserJSON struct {
			Username   string `json:"username"`
			Role       string `json:"role"`
			CreatedAt  string `json:"created_at"`
			PoolAccess bool   `json:"pool_access"`
		}
		var adminUsers []adminUserJSON
		if userInfo != nil && userInfo.Role == "admin" {
			for _, u := range readHtpasswdUsers() {
				au := adminUserJSON{Username: u, Role: "viewer"}
				if info, ok := doc.Users[u]; ok {
					au.Role = info.Role
					au.CreatedAt = info.CreatedAt
					au.PoolAccess = info.PoolAccess
				}
				adminUsers = append(adminUsers, au)
			}
		} else {
			// Regular users only see themselves
			if curInfo, ok := doc.Users[username]; ok {
				adminUsers = []adminUserJSON{{Username: username, Role: curInfo.Role, CreatedAt: curInfo.CreatedAt, PoolAccess: curInfo.PoolAccess}}
			} else {
				adminUsers = []adminUserJSON{{Username: username, Role: "viewer"}}
			}
		}

		writeJSON(w, map[string]any{
			"xrayClients": filtered, "generatedJSON": genJSON, "adminUsers": adminUsers,
		})
	}
}

func apiAddXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

		var body struct {
			UUID string `json:"uuid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UUID == "" {
			writeError(w, "UUID required", 400)
			return
		}
		if !validUUID.MatchString(body.UUID) {
			writeError(w, "Invalid UUID format (use lowercase hex, e.g. 550e8400-e29b-41d4-a716-446655440000)", 400)
			return
		}

		isAdmin := userInfo == nil || userInfo.Role == "admin"
		entry := xrayClientEntry{ID: body.UUID, Owner: username, Email: username}
		var err error
		if isAdmin {
			err = addXrayClient(entry)
		} else {
			var added bool
			added, err = addXrayClientIfOwnerUnique(entry)
			if !added && err == nil {
				writeError(w, "You can only create one client. Delete the existing one first.", 400)
				return
			}
		}
		if err != nil {
			log.Printf("Failed to save client: %v", err)
			writeInternalError(w, err)
			return
		}

		// Ensure the owner has a proxy password for xhttp auth
		if _, err := getOrCreateProxyPassword(username); err != nil {
			log.Printf("WARNING: failed to create proxy password for %s: %v", username, err)
		}

		// genXrayConfig() internally holds xrayClientsMu for the entire read+write,
		// so add/delete operations naturally serialize through the clients lock.
		if err := genXrayConfig(); err != nil {
			log.Printf("Failed to regenerate xray config: : %v", err)
			writeInternalError(w, err)
			return
		}
		if err := restartXray(r.Context(), cli); err != nil {
			log.Printf("WARNING: failed to restart xray after add: %v", err)
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiDeleteXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

		var body struct {
			UUID string `json:"uuid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UUID == "" {
			writeError(w, "UUID required", 400)
			return
		}

		// deleteXrayClientIfOwner 原子地检查所有权并删除，避免 TOCTOU 竞态
		isAdmin := userInfo == nil || userInfo.Role == "admin"
		ownerFilter := ""
		if !isAdmin {
			ownerFilter = username
		}
		deleted, err := deleteXrayClientIfOwner(body.UUID, ownerFilter)
		if err != nil {
			if err == errForbidden {
				writeError(w, "Forbidden: not your client", 403)
				return
			}
			log.Printf("Failed to delete client: %v", err)
			writeInternalError(w, err)
			return
		}
		if !deleted {
			writeError(w, "Client not found", 404)
			return
		}

		// genXrayConfig() internally holds xrayClientsMu for the entire read+write,
		// so add/delete operations naturally serialize through the clients lock.
		if err := genXrayConfig(); err != nil {
			log.Printf("Failed to regenerate xray config: : %v", err)
			writeInternalError(w, err)
			return
		}
		if err := restartXray(r.Context(), cli); err != nil {
			log.Printf("WARNING: failed to restart xray after delete: %v", err)
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

// GET /api/admin/users/xray/config/{uuid} — generate config for a specific client
func apiXrayClientConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)
		uuid := r.PathValue("uuid")
		if uuid == "" {
			writeError(w, "UUID required", 400)
			return
		}

		allClients, err := readXrayClientMaps()
		if err != nil {
			log.Printf("Failed to read clients: : %v", err)
			writeInternalError(w, err)
			return
		}
		var client map[string]string
		for _, c := range allClients {
			if c["id"] == uuid {
				client = c
				break
			}
		}
		if client == nil {
			writeError(w, "Client not found", 404)
			return
		}

		// Regular users can only view their own client
		if userInfo != nil && userInfo.Role != "admin" && client["owner"] != username {
			writeError(w, "Forbidden", 403)
			return
		}

		genJSON := handlers.GenerateClientConfig([]map[string]string{client}, handlers.ClientConfig{
			RealityDomain:     getEnv("XRAY_REALITY_DOMAIN"),
			RealityServerName: getEnv("XRAY_REALITY_SERVER_NAME"),
			RealityPublicKey:  getEnv("XRAY_PUBLIC_KEY"),
			RealityShortID:    getEnv("XRAY_SHORT_IDS"),
			XHTTPDomain:       getEnv("XHTTP_DOMAIN"),
			XHTTPProxyAuth:    proxyAuthForUser(client["owner"]),
		})
		writeJSON(w, map[string]any{
			"config": genJSON, "uuid": uuid, "owner": client["owner"],
		})
	}
}

var htpasswdFile = "/app/htpasswd"

// ipv6RotateMu 保护 IPv6 轮转操作，防止并发轮转
var ipv6RotateMu sync.Mutex
var htpasswdMu sync.Mutex
var validUsername = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var validUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUsername(username string) bool {
	return username != "" && validUsername.MatchString(username) && len(username) <= 64
}

func htpasswdAddUser(username, password string) error {
	if !isValidUsername(username) {
		return fmt.Errorf("invalid username")
	}
	htpasswdMu.Lock()
	defer htpasswdMu.Unlock()

	out, err := exec.Command("htpasswd", "-nbB", username, password).Output()
	if err != nil {
		return err
	}
	newLine := strings.TrimSpace(string(out))
	data, readErr := os.ReadFile(htpasswdFile)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	var lines []string
	replaced := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, username+":") {
			lines = append(lines, newLine)
			replaced = true
		} else {
			lines = append(lines, line)
		}
	}
	if !replaced {
		lines = append(lines, newLine)
	}
	// Atomic write via temp file + rename
	content := strings.Join(lines, "\n") + "\n"
	tmp := htpasswdFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, htpasswdFile)
}

func apiChangePassword(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)
		var body struct {
			Username    string `json:"username"`
			OldPassword string `json:"old_password"`
			Password    string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			writeError(w, "Username and password required", 400)
			return
		}
		// Non-admin can only change their own password
		if userInfo != nil && userInfo.Role != "admin" && body.Username != username {
			writeError(w, "Forbidden: can only change your own password", 403)
			return
		}
		// Verify old password for non-admin users
		if userInfo == nil || userInfo.Role != "admin" {
			if body.OldPassword == "" {
				writeError(w, "Old password required", 400)
				return
			}
			out, err := exec.Command("htpasswd", "-v", "-b", htpasswdFile, body.Username, body.OldPassword).Output()
			if err != nil || !strings.Contains(string(out), "login succeeded") {
				writeError(w, "Invalid old password", 401)
				return
			}
		}
		if err := htpasswdAddUser(body.Username, body.Password); err != nil {
			writeError(w, "Failed to update password", 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiAddAdmin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			writeError(w, "Username and password required", 400)
			return
		}
		if !isValidUsername(body.Username) {
			writeError(w, "Invalid username (use alphanumeric, underscore, hyphen)", 400)
			return
		}
		if body.Role == "" {
			body.Role = "viewer"
		}
		if body.Role != "admin" && body.Role != "viewer" {
			writeError(w, "Role must be admin or viewer", 400)
			return
		}
		// Only admin can create admin users
		userInfo, _ := getUserFromContext(r)
		if body.Role == "admin" && userInfo != nil && userInfo.Role != "admin" {
			writeError(w, "Forbidden", 403)
			return
		}
		if err := htpasswdAddUser(body.Username, body.Password); err != nil {
			writeInternalError(w, err)
			return
		}
		// Create users.json entry
		if err := updateUserMeta(body.Username, UserInfo{
			Role:      body.Role,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiDeleteAdmin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username required", 400)
			return
		}
		if isProtectedAdmin(body.Username) {
			writeError(w, "Cannot delete the default admin account", 403)
			return
		}
		_, currentUser := getUserFromContext(r)
		if body.Username == currentUser {
			writeError(w, "Cannot delete yourself", 400)
			return
		}

		// Prevent deleting the last admin
		doc, err := readUsersMeta()
		if err != nil {
			writeError(w, "Failed to read users", 500)
			return
		}
		adminCount := 0
		for _, info := range doc.Users {
			if info.Role == "admin" {
				adminCount++
			}
		}
		if targetInfo, ok := doc.Users[body.Username]; ok && targetInfo.Role == "admin" && adminCount <= 1 {
			writeError(w, "Cannot delete the last admin user", 400)
			return
		}

		htpasswdMu.Lock()
		data, readErr := os.ReadFile(htpasswdFile)
		if readErr != nil && !os.IsNotExist(readErr) {
			htpasswdMu.Unlock()
			writeError(w, "Failed to read htpasswd", 500)
			return
		}
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, body.Username+":") {
				lines = append(lines, line)
			}
		}
		content := strings.Join(lines, "\n") + "\n"
		tmp := htpasswdFile + ".tmp"
		if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
			htpasswdMu.Unlock()
			writeError(w, "Failed to write htpasswd", 500)
			return
		}
		if err := os.Rename(tmp, htpasswdFile); err != nil {
			htpasswdMu.Unlock()
			writeError(w, "Failed to update htpasswd", 500)
			return
		}
		htpasswdMu.Unlock()
		deleteUserMeta(body.Username)
		cleanupCliproxyForUser(r.Context(), cli, body.Username)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiSetRole(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username and role required", 400)
			return
		}
		if body.Role != "admin" && body.Role != "viewer" {
			writeError(w, "Role must be admin or viewer", 400)
			return
		}

		// Protected admin account cannot be demoted
		if isProtectedAdmin(body.Username) && body.Role != "admin" {
			writeError(w, "Cannot demote the default admin account", 403)
			return
		}

		// Prevent demoting the last admin
		if body.Role != "admin" {
			doc, err := readUsersMeta()
			if err != nil {
				writeError(w, "Failed to read users", 500)
				return
			}
			adminCount := 0
			for username, info := range doc.Users {
				if info.Role == "admin" && username != body.Username {
					adminCount++
				}
			}
			if adminCount == 0 {
				writeError(w, "Cannot demote the last admin user", 400)
				return
			}
		}

		existing, _ := ensureUserMeta(body.Username)
		if existing == nil {
			writeError(w, "User not found", 404)
			return
		}
		existing.Role = body.Role
		if err := updateUserMeta(body.Username, *existing); err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiWhoAmI(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, username := getUserFromContext(r)
		if info == nil {
			writeError(w, "Unauthorized", 401)
			return
		}
		writeJSON(w, map[string]any{
			"username":   username,
			"role":       info.Role,
			"created_at": info.CreatedAt,
		})
	}
}

func readHtpasswdUsers() []string {
	data, err := os.ReadFile(htpasswdFile)
	if err != nil {
		return nil
	}
	var users []string
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, ":"); idx > 0 {
			users = append(users, line[:idx])
		}
	}
	return users
}

// -- Network / IPv6 rotation APIs --

// loadCurrentIPs returns the current IPv6 for each container, queried from
// Cloudflare DNS. Falls back to env var static IPs.
func loadCurrentIPs() (xrayIP, nginxIP string) {
	xrayIP = getEnv("XRAY_IPV6")
	nginxIP = getEnv("NGINX_IPV6")
	if getEnv("CF_API_TOKEN") == "" {
		return
	}
	for _, di := range allDomainInfos() {
		rec, err := cfFindAAAARecord(di.Domain)
		if err != nil || rec == nil {
			continue
		}
		if di.Container == "xray" {
			xrayIP = rec.Content
		} else {
			nginxIP = rec.Content
		}
	}
	return
}

// saveCurrentIPs persists the working rotating IPs locally for use during next rotation's cleanup.
// DNS is the authoritative source; this file serves as a fast local cache.
func saveCurrentIPs(xrayIP, nginxIP string) {
	data, _ := json.Marshal(map[string]string{"xray": xrayIP, "nginx": nginxIP})
	_ = os.WriteFile("/app/data/rotating-ips.json", data, 0600)
}

// cleanContainerRotatingIPs removes all IPs in the rotating range (:f::/80) from a container,
// except the static IP.
func cleanContainerRotatingIPs(ctx context.Context, cli *client.Client, containerID, prefix, staticIP string) {
	containerExec(ctx, cli, containerID, "sh", "-c",
		fmt.Sprintf("ip -6 addr show | grep 'inet6 %sf:' | awk '{print $2}' | while read ip; do "+
			"case \"$ip\" in %s*) continue ;; esac; "+
			"ip addr del \"$ip\" dev eth0 2>/dev/null; done", prefix, staticIP))
}

func apiNetworkStatus(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfOK := getEnv("CF_API_TOKEN") != ""
		subnet := getEnv("IPV6_SUBNET")
		nginxStatic := getEnv("NGINX_IPV6")
		xrayStatic := getEnv("XRAY_IPV6")
		xrayIP, nxIP := loadCurrentIPs()

		// Derive allocation ranges from subnet's first /64
		prefix := ""
		staticRange := ""
		rotatingRange := ""
		if _, ipNet, err := net.ParseCIDR(subnet); err == nil {
			ip := ipNet.IP.To16()
			if ip != nil && len(ip) == 16 {
				parts := make([]string, 4)
				for i := 0; i < 4; i++ {
					parts[i] = fmt.Sprintf("%x", binary.BigEndian.Uint16(ip[i*2:i*2+2]))
				}
				prefix = strings.Join(parts, ":") + ":"
				staticRange = prefix + "1::/80"
				rotatingRange = prefix + "f::/80"
			}
		}

		// CF DNS records for all domains
		type dnsInfo struct {
			Domain string       `json:"domain"`
			Record *cfDNSRecord `json:"record"`
			Error  string       `json:"error,omitempty"`
			Synced bool         `json:"synced"`
		}
		var dnsResults []dnsInfo
		if cfOK {
			for _, di := range allDomainInfos() {
				info := dnsInfo{Domain: di.Domain}
				expectedIP := nxIP
				if di.Container == "xray" {
					expectedIP = xrayIP
				}
				rec, err := cfFindAAAARecord(di.Domain)
				if err != nil {
					info.Error = err.Error()
				} else {
					info.Record = rec
					if rec != nil {
						info.Synced = rec.Content == expectedIP
					}
				}
				dnsResults = append(dnsResults, info)
			}
		}

		writeJSON(w, map[string]any{
			"subnet":         prefix + "/64",
			"static_range":   staticRange,
			"rotating_range": rotatingRange,
			"nginx_ip":       nginxStatic,
			"xray_static":    xrayStatic,
			"xray_ip":        xrayIP,
			"nginx_rotating": nxIP,
			"cf_configured":  cfOK,
			"dns_records":    dnsResults,
		})
	}
}

// findContainer returns the container ID for a given compose service label.
func findContainer(ctx context.Context, cli *client.Client, service string) (string, error) {
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{
			Key: "label", Value: "com.docker.compose.service=" + service,
		}),
	})
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", fmt.Errorf("container not found: %s", service)
	}
	return containers[0].ID, nil
}

// addContainerIP adds an IPv6 to the container's default-route interface.
func addContainerIP(ctx context.Context, cli *client.Client, containerID, ip string) error {
	return containerExec(ctx, cli, containerID, "sh", "-c",
		fmt.Sprintf("ip addr add %s/64 dev $(ip -6 route show default | grep -o 'dev [^ ]*' | awk '{print $2; exit}')", ip))
}
func delContainerIP(ctx context.Context, cli *client.Client, containerID, ip string) {
	containerExec(ctx, cli, containerID, "sh", "-c",
		fmt.Sprintf("ip addr del %s/64 dev $(ip -6 route show default | grep -o 'dev [^ ]*' | awk '{print $2; exit}') 2>/dev/null", ip))
}

// allDomainInfos returns all managed domains and their container assignments.
// XRAY_REALITY_DOMAIN → xray, NGINX_DOMAINS (comma-separated) → nginx.
func allDomainInfos() []struct{ Domain, Container string } {
	var infos []struct{ Domain, Container string }
	if d := getEnv("XRAY_REALITY_DOMAIN"); d != "" {
		infos = append(infos, struct{ Domain, Container string }{d, "xray"})
	}
	if d := getEnv("XHTTP_DOMAIN"); d != "" {
		infos = append(infos, struct{ Domain, Container string }{d, "nginx"})
	}
	for _, d := range strings.Split(getEnv("NGINX_DOMAINS"), ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			infos = append(infos, struct{ Domain, Container string }{d, "nginx"})
		}
	}
	return infos
}

// startupRotateIPv6 assigns fresh rotating IPv6 addresses after container restart.
// Called on admin startup to restore rotation-based DNS after containers are recreated.
func startupRotateIPv6(cli *client.Client) {
	if getEnv("CF_API_TOKEN") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	xrayID, err := findContainer(ctx, cli, "xray")
	if err != nil {
		log.Printf("startup rotate: xray: %v", err)
		return
	}
	nginxID, err := findContainer(ctx, cli, "nginx")
	if err != nil {
		log.Printf("startup rotate: nginx: %v", err)
		return
	}

	subnet := getEnv("IPV6_SUBNET")
	prefix, err := parseSubnetPrefix(subnet)
	if err != nil {
		log.Printf("startup rotate: subnet: %v", err)
		return
	}
	xrayStatic := getEnv("XRAY_IPV6")
	nginxStatic := getEnv("NGINX_IPV6")

	// Clean up any leftover rotating IPs before assigning new ones,
	// ensuring container state matches DNS state.
	cleanContainerRotatingIPs(ctx, cli, xrayID, prefix, xrayStatic)
	cleanContainerRotatingIPs(ctx, cli, nginxID, prefix, nginxStatic)

	xrayAssigned := getContainerIPs(ctx, cli, xrayID)
	xrayExclude := append([]string{xrayStatic, nginxStatic}, xrayAssigned...)
	xrayIP, err := genRandomIPv6(prefix, xrayExclude...)
	if err != nil {
		log.Printf("startup rotate: xray IP: %v", err)
		return
	}

	nginxAssigned := getContainerIPs(ctx, cli, nginxID)
	nginxExclude := append([]string{xrayStatic, nginxStatic, xrayIP}, nginxAssigned...)
	nginxIP, err := genRandomIPv6(prefix, nginxExclude...)
	if err != nil {
		log.Printf("startup rotate: nginx IP: %v", err)
		return
	}

	// Add new IPs first, then update DNS. If DNS fails, roll back IPs.
	if err := addContainerIP(ctx, cli, xrayID, xrayIP); err != nil {
		log.Printf("startup rotate: add xray IP: %v", err)
		return
	}
	if err := addContainerIP(ctx, cli, nginxID, nginxIP); err != nil {
		delContainerIP(ctx, cli, xrayID, xrayIP)
		log.Printf("startup rotate: add nginx IP: %v", err)
		return
	}

	for _, di := range allDomainInfos() {
		ip := nginxIP
		if di.Container == "xray" {
			ip = xrayIP
		}
		if _, err := cfUpsertAAAARecord(di.Domain, ip); err != nil {
			// DNS update failed — roll back new IPs so containers match DNS state
			delContainerIP(ctx, cli, xrayID, xrayIP)
			delContainerIP(ctx, cli, nginxID, nginxIP)
			log.Printf("startup rotate: DNS %s: %v — rolled back IPs", di.Domain, err)
			return
		}
	}

	// Persist current IPs for next rotation cleanup
	saveCurrentIPs(xrayIP, nginxIP)

	log.Printf("Startup rotate: xray=%s nginx=%s", xrayIP, nginxIP)
}

func apiRotateIPv6(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ipv6RotateMu.Lock()
		defer ipv6RotateMu.Unlock()

		subnet := getEnv("IPV6_SUBNET")
		xrayStatic := getEnv("XRAY_IPV6")
		nginxStatic := getEnv("NGINX_IPV6")

		if getEnv("CF_API_TOKEN") == "" {
			writeError(w, "CF_API_TOKEN not configured", 400)
			return
		}

		xrayID, err := findContainer(r.Context(), cli, "xray")
		if err != nil {
			writeError(w, "Xray container not found", 500)
			return
		}
		nginxID, err := findContainer(r.Context(), cli, "nginx")
		if err != nil {
			writeError(w, "Nginx container not found", 500)
			return
		}

		// Collect assigned IPs to avoid collisions; generate separate IPs per container
		prefix, err := parseSubnetPrefix(subnet)
		if err != nil {
			log.Printf("Invalid IPV6_SUBNET: : %v", err)
			writeInternalError(w, err)
			return
		}

		xrayAssigned := getContainerIPs(r.Context(), cli, xrayID)
		xrayExclude := append([]string{xrayStatic, nginxStatic}, xrayAssigned...)
		xrayIP, err := genRandomIPv6(prefix, xrayExclude...)
		if err != nil {
			log.Printf("Failed to generate xray IPv6: : %v", err)
			writeInternalError(w, err)
			return
		}

		nginxAssigned := getContainerIPs(r.Context(), cli, nginxID)
		nginxExclude := append([]string{xrayStatic, nginxStatic, xrayIP}, nginxAssigned...)
		nginxIP, err := genRandomIPv6(prefix, nginxExclude...)
		if err != nil {
			log.Printf("Failed to generate nginx IPv6: : %v", err)
			writeInternalError(w, err)
			return
		}

		oldXrayIP, oldNginxIP := loadCurrentIPs()

		// Add new IPs
		if err := addContainerIP(r.Context(), cli, xrayID, xrayIP); err != nil {
			log.Printf("Failed to add IPv6 to xray: : %v", err)
			writeInternalError(w, err)
			return
		}
		if err := addContainerIP(r.Context(), cli, nginxID, nginxIP); err != nil {
			delContainerIP(r.Context(), cli, xrayID, xrayIP)
			log.Printf("Failed to add IPv6 to nginx: : %v", err)
			writeInternalError(w, err)
			return
		}

		// Update DNS per domain → container mapping
		domains := allDomainInfos()
		var updated []string
		for _, di := range domains {
			ip := nginxIP
			if di.Container == "xray" {
				ip = xrayIP
			}
			if _, err := cfUpsertAAAARecord(di.Domain, ip); err != nil {
				delContainerIP(r.Context(), cli, xrayID, xrayIP)
				delContainerIP(r.Context(), cli, nginxID, nginxIP)
				log.Printf("DNS update failed for %s: %v", di.Domain, err)
				writeError(w, fmt.Sprintf("DNS update failed for %s", di.Domain), 500)
				return
			}
			updated = append(updated, fmt.Sprintf("%s → %s", di.Domain, ip))
		}

		// Remove old rotating IPs
		if oldXrayIP != "" && oldXrayIP != xrayStatic {
			delContainerIP(r.Context(), cli, xrayID, oldXrayIP)
		}
		if oldNginxIP != "" && oldNginxIP != nginxStatic {
			delContainerIP(r.Context(), cli, nginxID, oldNginxIP)
		}

		// state now lives in Cloudflare DNS

		writeJSON(w, map[string]any{
			"xray_ip":  xrayIP,
			"nginx_ip": nginxIP,
			"domains":  updated,
		})
	}
}

func parseSubnetPrefix(subnet string) (string, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parse subnet: %w", err)
	}
	// Return the first 64 bits as hex string with trailing ::
	ip := ipNet.IP.To16()
	if ip == nil {
		return "", fmt.Errorf("not an IPv6 subnet")
	}
	// Build prefix like "2605:a141:2322:2623:1::"
	parts := make([]string, 4)
	for i := 0; i < 4; i++ {
		parts[i] = fmt.Sprintf("%x", binary.BigEndian.Uint16(ip[i*2:i*2+2]))
	}
	return strings.Join(parts, ":") + ":", nil
}

func genRandomIPv6(prefix string, exclude ...string) (string, error) {
	excludeSet := map[string]bool{}
	for _, e := range exclude {
		if e != "" {
			excludeSet[e] = true
		}
	}
	for attempts := 0; attempts < 100; attempts++ {
		var ifid [8]byte
		if _, err := io.ReadFull(rand.Reader, ifid[:]); err != nil {
			return "", err
		}
		// 5th hextet fixed to 000f → all rotating IPs live in :f::/80,
		// never colliding with static IPs in :1::/80 range.
		ip := fmt.Sprintf("%s%x:%x:%x:%x",
			prefix,
			0xf,
			binary.BigEndian.Uint16(ifid[0:2]),
			binary.BigEndian.Uint16(ifid[2:4]),
			binary.BigEndian.Uint16(ifid[4:6]),
		)
		if !excludeSet[ip] {
			return ip, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique IPv6 after 100 attempts")
}

func apiSyncDNS(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if getEnv("CF_API_TOKEN") == "" {
			writeError(w, "CF_API_TOKEN not configured", 400)
			return
		}
		// Get actual container IPs — no local state, containers are the source of truth.
		xrayID, err := findContainer(r.Context(), cli, "xray")
		if err != nil {
			log.Printf("xray container: : %v", err)
			writeInternalError(w, err)
			return
		}
		nginxID, err := findContainer(r.Context(), cli, "nginx")
		if err != nil {
			log.Printf("nginx container: : %v", err)
			writeInternalError(w, err)
			return
		}

		xrayIP := getContainerIP(r.Context(), cli, xrayID)
		if xrayIP == "" {
			xrayIP = getEnv("XRAY_IPV6")
		}
		nginxIP := getContainerIP(r.Context(), cli, nginxID)
		if nginxIP == "" {
			nginxIP = getEnv("NGINX_IPV6")
		}

		var results []string
		for _, di := range allDomainInfos() {
			ip := nginxIP
			if di.Container == "xray" {
				ip = xrayIP
			}
			if _, err := cfUpsertAAAARecord(di.Domain, ip); err != nil {
				results = append(results, fmt.Sprintf("%s: %s", di.Domain, err.Error()))
			} else {
				results = append(results, fmt.Sprintf("%s → %s synced", di.Domain, ip))
			}
		}
		writeJSON(w, map[string]any{"results": results})
	}
}

// getContainerIPs returns all global IPv6 addresses in our subnet from a container.
func getContainerIPs(ctx context.Context, cli *client.Client, containerID string) []string {
	subnet := getEnv("IPV6_SUBNET")
	prefix, _ := parseSubnetPrefix(subnet)
	var buf bytes.Buffer
	execConfig := container.ExecOptions{
		Cmd:          []string{"sh", "-c", "ip -6 -o addr show 2>/dev/null | awk '{print $4}' | cut -d/ -f1"},
		AttachStdout: true, AttachStderr: true,
	}
	execResp, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return nil
	}
	attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return nil
	}
	defer attachResp.Close()
	stdcopy.StdCopy(&buf, io.Discard, attachResp.Reader)
	var ips []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		ip := strings.TrimSpace(line)
		if ip != "" && strings.HasPrefix(ip, prefix) && !strings.Contains(ip, "::1") {
			ips = append(ips, ip)
		}
	}
	return ips
}

// getContainerIP returns the first global IPv6 on the given interface.
func getContainerIP(ctx context.Context, cli *client.Client, containerID string) string {
	ips := getContainerIPs(ctx, cli, containerID)
	if ips == nil {
		return ""
	}
	subnet := getEnv("IPV6_SUBNET")
	prefix, err := parseSubnetPrefix(subnet)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if strings.HasPrefix(ip, prefix) && !strings.Contains(ip, "::1") {
			return ip
		}
	}
	return ""
}

func restartXray(ctx context.Context, cli *client.Client) error {
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{
			Key: "label", Value: "com.docker.compose.service=xray",
		}),
	})
	if err != nil {
		return fmt.Errorf("list xray container: %w", err)
	}
	if len(containers) == 0 {
		return nil
	}
	id := containers[0].ID

	t := 10
	if err := cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &t}); err != nil {
		return fmt.Errorf("restart xray: %w", err)
	}

	// Restore the rotating IPv6 address after restart.
	restoreRotatingIP(ctx, cli, id)
	return nil
}

// restoreRotatingIP re-adds the dynamic rotating IPv6 address after a container restart.
// Docker restart drops dynamically-assigned IPs added via `ip addr add`,
// but admin knows the current IP from DNS and can re-add it.
func restoreRotatingIP(ctx context.Context, cli *client.Client, containerID string) {
	if getEnv("CF_API_TOKEN") == "" {
		return
	}
	xrayIP, _ := loadCurrentIPs()
	if xrayIP == "" {
		return
	}
	if err := addContainerIP(ctx, cli, containerID, xrayIP); err != nil {
		log.Printf("WARNING: failed to restore rotating IPv6 %s: %v", xrayIP, err)
	}
}

func containerExec(ctx context.Context, cli *client.Client, containerID string, cmd ...string) error {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("exec create: %w", err)
	}
	attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("exec attach: %w", err)
	}
	defer attachResp.Close()
	var stderrBuf bytes.Buffer
	stdcopy.StdCopy(io.Discard, &stderrBuf, attachResp.Reader)
	inspect, err := cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return fmt.Errorf("exec inspect: %w", err)
	}
	if inspect.ExitCode != 0 {
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg == "" {
			errMsg = fmt.Sprintf("exit code %d", inspect.ExitCode)
		}
		return fmt.Errorf("exec %v: %s", cmd, errMsg)
	}
	return nil
}

// GET /api/admin/services/{name}/logs?tail=100
func apiServiceLogs(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "100"
		}
		if n, err := strconv.Atoi(tail); err != nil || n <= 0 || n > 10000 {
			tail = "100"
		}
		containers, err := cli.ContainerList(r.Context(), container.ListOptions{
			All: true,
			Filters: filters.NewArgs(filters.KeyValuePair{
				Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", name),
			}),
		})
		if err != nil || len(containers) == 0 {
			writeError(w, "Service not found", 404)
			return
		}
		logs, err := cli.ContainerLogs(r.Context(), containers[0].ID, container.LogsOptions{
			ShowStdout: true, ShowStderr: true,
			Tail: tail, Timestamps: true,
		})
		if err != nil {
			writeInternalError(w, err)
			return
		}
		defer logs.Close()
		var buf bytes.Buffer
		stdcopy.StdCopy(&buf, &buf, logs)
		s := buf.String()
		// Keep last 128KB max
		if len(s) > 128*1024 {
			s = s[len(s)-128*1024:]
		}
		writeJSON(w, map[string]any{"logs": s})
	}
}

// GET /api/admin/services/{name}/logs/stream?tail=100
func apiServiceLogStream(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "100"
		}
		if n, err := strconv.Atoi(tail); err != nil || n <= 0 || n > 10000 {
			tail = "100"
		}
		containers, err := cli.ContainerList(r.Context(), container.ListOptions{
			All: true,
			Filters: filters.NewArgs(filters.KeyValuePair{
				Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", name),
			}),
		})
		if err != nil || len(containers) == 0 {
			writeError(w, "Service not found", 404)
			return
		}
		logs, err := cli.ContainerLogs(r.Context(), containers[0].ID, container.LogsOptions{
			ShowStdout: true, ShowStderr: true,
			Tail: tail, Timestamps: true, Follow: true,
		})
		if err != nil {
			writeInternalError(w, err)
			return
		}
		defer logs.Close()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, "Streaming not supported", 500)
			return
		}

		// Decode Docker multiplexed stream and write as SSE
		const maxFrameSize = 1 << 20 // 1MB 上限防止 OOM
		buf := make([]byte, 4096)
		type frame struct {
			data []byte
			err  error
		}
		ch := make(chan frame, 1)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel() // 客户端断开时通知 readFrame 退出

		readFrame := func() {
			hdr := make([]byte, 8)
			if _, err := io.ReadFull(logs, hdr); err != nil {
				ch <- frame{nil, err}
				return
			}
			size := int(binary.BigEndian.Uint32(hdr[4:8]))
			if size > maxFrameSize {
				ch <- frame{nil, fmt.Errorf("frame too large: %d bytes", size)}
				return
			}
			if size > cap(buf) {
				buf = make([]byte, size)
			} else {
				buf = buf[:size]
			}
			if size == 0 {
				ch <- frame{nil, nil}
				return
			}
			if _, err := io.ReadFull(logs, buf[:size]); err != nil {
				ch <- frame{nil, err}
				return
			}
			select {
			case <-ctx.Done():
				// 客户端已断开，丢弃数据并退出
			case ch <- frame{buf[:size], nil}:
			}
		}

		go readFrame()
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-ch:
				if f.err != nil {
					return
				}
				if f.data == nil {
					go readFrame()
					continue
				}
				line := strings.TrimRight(string(f.data), "\n\r")
				if line != "" {
					for _, part := range strings.Split(line, "\n") {
						// 转义 \r 防止 SSE 注入
						escaped := strings.ReplaceAll(part, "\r", "")
						fmt.Fprintf(w, "data: %s\n", escaped)
					}
					fmt.Fprint(w, "\n")
					flusher.Flush()
				}
				go readFrame()
			}
		}
	}
}
