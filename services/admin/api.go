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

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}


func apiDashboard(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

		// For non-admin users, return personalized dashboard
		if userInfo != nil && userInfo.Role != "admin" {
			allClients, _ := handlers.ReadXrayClients()
			var myClients []map[string]string
			for _, c := range allClients {
				if c["owner"] == username {
					myClients = append(myClients, c)
				}
			}
			genJSON := handlers.GenerateClientConfig(myClients, handlers.ClientConfig{
				RealityDomain:     getEnv("XRAY_REALITY_DOMAIN"),
				RealityServerName: getEnv("XRAY_REALITY_SERVER_NAME"),
				RealityPublicKey:  getEnv("XRAY_PUBLIC_KEY"),
				RealityShortID:    getEnv("XRAY_SHORT_IDS"),
				XHTTPDomain:       getEnv("XHTTP_DOMAIN"),
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
		default:
			writeError(w, "Unknown action", 400)
			return
		}
		if err != nil {
			writeError(w, err.Error(), 500)
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
		allClients, _ := handlers.ReadXrayClients()

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

		genJSON := handlers.GenerateClientConfig(filtered, handlers.ClientConfig{
			RealityDomain:     getEnv("XRAY_REALITY_DOMAIN"),
			RealityServerName: getEnv("XRAY_REALITY_SERVER_NAME"),
			RealityPublicKey:  getEnv("XRAY_PUBLIC_KEY"),
			RealityShortID:    getEnv("XRAY_SHORT_IDS"),
			XHTTPDomain:       getEnv("XHTTP_DOMAIN"),
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

		// Regular users can only create one client
		if userInfo != nil && userInfo.Role != "admin" {
			allClients, _ := handlers.ReadXrayClients()
			for _, c := range allClients {
				if c["owner"] == username {
					writeError(w, "You can only create one client. Delete the existing one first.", 400)
					return
				}
			}
		}

		xrayConfigMu.Lock()
		defer xrayConfigMu.Unlock()

		data, err := os.ReadFile("/app/configs/xray.json")
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		var full map[string]any
		if err := json.Unmarshal(data, &full); err != nil {
			writeError(w, "Invalid xray config: "+err.Error(), 500)
			return
		}
		inbounds, _ := full["inbounds"].([]any)
		for i := range inbounds {
			ib, _ := inbounds[i].(map[string]any)
			tag, _ := ib["tag"].(string)
			settings, _ := ib["settings"].(map[string]any)
			if settings == nil {
				continue
			}
			clientsRaw, _ := settings["clients"].([]any)
			nc := map[string]any{"id": body.UUID, "email": username, "owner": username}
			if tag == "reality-in" {
				nc["flow"] = "xtls-rprx-vision"
			}
			clientsRaw = append(clientsRaw, nc)
			settings["clients"] = clientsRaw
			settings["decryption"] = "none"
		}
		out, err := json.MarshalIndent(full, "", "  ")
		if err != nil {
			writeError(w, "Failed to serialize config", 500)
			return
		}
		if err := os.WriteFile("/app/configs/xray.json", out, 0600); err != nil {
				log.Printf("ERROR: failed to write xray config: %v", err)
				writeError(w, "Failed to save xray config", 500)
				return
			}

		if err := restartXray(cli); err != nil {
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
		xrayConfigMu.Lock()
		defer xrayConfigMu.Unlock()

		data, err := os.ReadFile("/app/configs/xray.json")
		if err != nil {
			writeError(w, "Failed to read xray config: "+err.Error(), 500)
			return
		}
		var full map[string]any
		if err := json.Unmarshal(data, &full); err != nil {
			writeError(w, "Invalid xray config: "+err.Error(), 500)
			return
		}
		inbounds, _ := full["inbounds"].([]any)
		for i := range inbounds {
			ib, _ := inbounds[i].(map[string]any)
			settings, _ := ib["settings"].(map[string]any)
			if settings == nil {
				continue
			}
			clientsRaw, _ := settings["clients"].([]any)
			var kept []any
			for _, c := range clientsRaw {
				cm, _ := c.(map[string]any)
				id, _ := cm["id"].(string)
				if id == body.UUID {
					// Check ownership
					owner, _ := cm["owner"].(string)
					if userInfo != nil && userInfo.Role != "admin" && owner != username {
						writeError(w, "Forbidden: not your client", 403)
						return
					}
					continue
				}
				kept = append(kept, c)
			}
			settings["clients"] = kept
		}
		out, err := json.MarshalIndent(full, "", "  ")
		if err != nil {
			writeError(w, "Failed to serialize config", 500)
			return
		}
		if err := os.WriteFile("/app/configs/xray.json", out, 0600); err != nil {
				log.Printf("ERROR: failed to write xray config: %v", err)
				writeError(w, "Failed to save xray config", 500)
				return
			}

		if err := restartXray(cli); err != nil {
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

		allClients, _ := handlers.ReadXrayClients()
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
		})
		writeJSON(w, map[string]any{
			"config": genJSON, "uuid": uuid, "owner": client["owner"],
		})
	}
}

var htpasswdFile = "/app/htpasswd"
var xrayConfigMu sync.Mutex
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
	data, _ := os.ReadFile(htpasswdFile)
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
			writeError(w, err.Error(), 500)
			return
		}
		// Create users.json entry
		if err := updateUserMeta(body.Username, UserInfo{
			Role:      body.Role,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			writeError(w, err.Error(), 500)
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
		data, _ := os.ReadFile(htpasswdFile)
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
		if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
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
		existing.Role = body.Role
		if err := updateUserMeta(body.Username, *existing); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiWhoAmI(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, username := getUserFromContext(r)
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

type rotatingIPState struct {
	XrayIP  string `json:"xray_ip"`
	NginxIP string `json:"nginx_ip"`
}

func loadRotatingState() rotatingIPState {
	var s rotatingIPState
	data, err := os.ReadFile("/app/data/rotating-ip.json")
	if err != nil {
		return s
	}
	json.Unmarshal(data, &s)
	return s
}

func saveRotatingState(s rotatingIPState) {
	b, _ := json.Marshal(s)
	os.WriteFile("/app/data/rotating-ip.json", b, 0644)
}

func apiNetworkStatus(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfOK := getEnv("CF_API_TOKEN") != ""
		subnet := getEnv("IPV6_SUBNET")
		nginxIP := getEnv("NGINX_IPV6")
		xrayPrimary := getEnv("XRAY_IPV6")
		state := loadRotatingState()
		xrayIP := state.XrayIP
		if xrayIP == "" {
			xrayIP = xrayPrimary
		}
		nxIP := state.NginxIP
		if nxIP == "" {
			nxIP = nginxIP
		}

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
			"nginx_ip":       nginxIP,
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

// detectIPvlanIface finds the ipvlan interface (IPv6 enabled) on a container.
func detectIPvlanIface(ctx context.Context, cli *client.Client, containerID string) string {
	for _, try := range []string{"eth0", "eth1", "eth2"} {
		var buf bytes.Buffer
		execConfig := container.ExecOptions{
			Cmd:          []string{"sh", "-c", fmt.Sprintf("cat /proc/sys/net/ipv6/conf/%s/disable_ipv6 2>/dev/null || echo 1", try)},
			AttachStdout: true,
			AttachStderr: true,
		}
		execResp, createErr := cli.ContainerExecCreate(ctx, containerID, execConfig)
		if createErr == nil {
			attachResp, attachErr := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
			if attachErr == nil {
				stdcopy.StdCopy(&buf, io.Discard, attachResp.Reader)
				attachResp.Close()
				if strings.TrimSpace(buf.String()) == "0" {
					return try
				}
			}
		}
	}
	return "eth1" // fallback
}

// allDomainInfos returns the DNS domain configurations that should be rotated.
// Each entry maps a domain to its container (xray or nginx).
func allDomainInfos() []struct{ Domain, Container string } {
	var infos []struct{ Domain, Container string }
	if d := getEnv("XRAY_REALITY_DOMAIN"); d != "" {
		infos = append(infos, struct{ Domain, Container string }{d, "xray"})
	}
	if d := getEnv("XHTTP_DOMAIN"); d != "" {
		infos = append(infos, struct{ Domain, Container string }{d, "nginx"})
	}
	return infos
}

func apiRotateIPv6(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		xrayConfigMu.Lock()
		defer xrayConfigMu.Unlock()

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
			writeError(w, "Invalid IPV6_SUBNET: "+err.Error(), 500)
			return
		}

		xrayAssigned, _ := collectContainerIPs(r.Context(), cli, xrayID)
		xrayExclude := append([]string{xrayStatic, nginxStatic}, xrayAssigned...)
		xrayIP, err := genRandomIPv6(prefix, xrayExclude...)
		if err != nil {
			writeError(w, "Failed to generate xray IPv6: "+err.Error(), 500)
			return
		}

		nginxAssigned, _ := collectContainerIPs(r.Context(), cli, nginxID)
		nginxExclude := append([]string{xrayStatic, nginxStatic, xrayIP}, nginxAssigned...)
		nginxIP, err := genRandomIPv6(prefix, nginxExclude...)
		if err != nil {
			writeError(w, "Failed to generate nginx IPv6: "+err.Error(), 500)
			return
		}

		oldState := loadRotatingState()
		xrayIface := detectIPvlanIface(r.Context(), cli, xrayID)
		nginxIface := detectIPvlanIface(r.Context(), cli, nginxID)

		// Add new IPs
		if err := containerExec(r.Context(), cli, xrayID, "ip", "addr", "add", xrayIP+"/64", "dev", xrayIface); err != nil {
			writeError(w, "Failed to add IPv6 to xray: "+err.Error(), 500)
			return
		}
		if err := containerExec(r.Context(), cli, nginxID, "ip", "addr", "add", nginxIP+"/64", "dev", nginxIface); err != nil {
			containerExec(r.Context(), cli, xrayID, "ip", "addr", "del", xrayIP+"/64", "dev", xrayIface)
			writeError(w, "Failed to add IPv6 to nginx: "+err.Error(), 500)
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
				containerExec(r.Context(), cli, xrayID, "ip", "addr", "del", xrayIP+"/64", "dev", xrayIface)
				containerExec(r.Context(), cli, nginxID, "ip", "addr", "del", nginxIP+"/64", "dev", nginxIface)
				writeError(w, fmt.Sprintf("DNS update failed for %s: %s", di.Domain, err.Error()), 500)
				return
			}
			updated = append(updated, fmt.Sprintf("%s → %s", di.Domain, ip))
		}

		// Remove old rotating IPs
		if oldState.XrayIP != "" && oldState.XrayIP != xrayStatic {
			containerExec(r.Context(), cli, xrayID, "ip", "addr", "del", oldState.XrayIP+"/64", "dev", xrayIface)
		}
		if oldState.NginxIP != "" && oldState.NginxIP != nginxStatic {
			containerExec(r.Context(), cli, nginxID, "ip", "addr", "del", oldState.NginxIP+"/64", "dev", nginxIface)
		}

		saveRotatingState(rotatingIPState{XrayIP: xrayIP, NginxIP: nginxIP})

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

// collectContainerIPs returns all non-link-local IPv6 addresses on the container's ipvlan interface.
func collectContainerIPs(ctx context.Context, cli *client.Client, containerID string) ([]string, error) {
	iface := detectIPvlanIface(ctx, cli, containerID)
	var buf bytes.Buffer
	execConfig := container.ExecOptions{
		Cmd:          []string{"sh", "-c", fmt.Sprintf("ip -6 -o addr show dev %s 2>/dev/null | awk '{print $4}' | cut -d/ -f1", iface)},
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return nil, err
	}
	attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, err
	}
	defer attachResp.Close()
	stdcopy.StdCopy(&buf, io.Discard, attachResp.Reader)
	var ips []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		ip := strings.TrimSpace(line)
		if ip != "" && !strings.HasPrefix(ip, "fe80:") {
			ips = append(ips, ip)
		}
	}
	return ips, nil
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
		state := loadRotatingState()
		xrayIP := state.XrayIP
		if xrayIP == "" {
			xrayIP = getEnv("XRAY_IPV6")
		}
		nginxIP := state.NginxIP
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

func restartXray(cli *client.Client) error {
	ctx := context.Background()
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
	t := 10
	if err := cli.ContainerRestart(ctx, containers[0].ID, container.StopOptions{Timeout: &t}); err != nil {
		return fmt.Errorf("restart xray: %w", err)
	}
	return nil
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
			writeError(w, err.Error(), 500)
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
			writeError(w, err.Error(), 500)
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
		hdr := make([]byte, 8)
		buf := make([]byte, 4096)
		for {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			_, err := io.ReadFull(logs, hdr)
			if err != nil {
				return
			}
			size := int(binary.BigEndian.Uint32(hdr[4:8]))
			if size > len(buf) {
				buf = make([]byte, size)
			}
			if size > 0 {
				_, err := io.ReadFull(logs, buf[:size])
				if err != nil {
					return
				}
				line := string(buf[:size])
				line = strings.TrimRight(line, "\n\r")
				if line != "" {
					fmt.Fprintf(w, "data: %s\n\n", line)
					flusher.Flush()
				}
			}
		}
	}
}
