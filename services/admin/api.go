package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
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
				XrayIPV6:          getEnv("XRAY_IPV6"),
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
			XrayIPV6:          getEnv("XRAY_IPV6"),
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
		_, username := getUserFromContext(r)

		var body struct{ UUID string `json:"uuid"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UUID == "" {
			writeError(w, "UUID required", 400)
			return
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
			nc := map[string]any{"id": body.UUID, "owner": username}
			if tag == "reality-in" {
				nc["flow"] = "xtls-rprx-vision"
			}
			clientsRaw = append(clientsRaw, nc)
			settings["clients"] = clientsRaw
			settings["decryption"] = "none"
		}
		out, _ := json.MarshalIndent(full, "", "  ")
		os.WriteFile("/app/configs/xray.json", out, 0644)
		go restartXray(cli)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiDeleteXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

		var body struct{ UUID string `json:"uuid"` }
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
					if owner != "" && userInfo != nil && userInfo.Role != "admin" && owner != username {
						writeError(w, "Forbidden: not your client", 403)
						return
					}
					continue
				}
				kept = append(kept, c)
			}
			settings["clients"] = kept
		}
		out, _ := json.MarshalIndent(full, "", "  ")
		os.WriteFile("/app/configs/xray.json", out, 0644)
		go restartXray(cli)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

var htpasswdFile = "/app/htpasswd"
var xrayConfigMu sync.Mutex
var htpasswdMu sync.Mutex

func htpasswdAddUser(username, password string) error {
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
	return os.WriteFile(htpasswdFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func apiChangePassword(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)
		var body struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			writeError(w, "Username and password required", 400)
			return
		}
		// Non-admin can only change their own password
		if userInfo != nil && userInfo.Role != "admin" && body.Username != username {
			writeError(w, "Forbidden: can only change your own password", 403)
			return
		}
		if err := htpasswdAddUser(body.Username, body.Password); err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiAddAdmin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password, Role string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			writeError(w, "Username and password required", 400)
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
		doc, _ := readUsersMeta()
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
		os.WriteFile(htpasswdFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)
		htpasswdMu.Unlock()
		deleteUserMeta(body.Username)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiSetRole(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Role string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
			writeError(w, "Username and role required", 400)
			return
		}
		if body.Role != "admin" && body.Role != "viewer" {
			writeError(w, "Role must be admin or viewer", 400)
			return
		}

		// Prevent demoting the last admin
		if body.Role != "admin" {
			doc, _ := readUsersMeta()
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
	CurrentIP string `json:"current_ip"`
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
		xrayDomain := getEnv("XRAY_REALITY_DOMAIN")
		state := loadRotatingState()
		xrayIP := state.CurrentIP
		if xrayIP == "" {
			xrayIP = xrayPrimary
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
					parts[i] = fmt.Sprintf("%x", binary.BigEndian.Uint16(ip[i*2 : i*2+2]))
				}
				prefix = strings.Join(parts, ":") + ":"
				staticRange = prefix + "1::/80"
				rotatingRange = prefix + "f::/80"
			}
		}

		// CF DNS records
		type dnsInfo struct {
			Domain  string        `json:"domain"`
			Record  *cfDNSRecord  `json:"record"`
			Error   string        `json:"error,omitempty"`
			Synced  bool          `json:"synced"`
		}
		var dnsReality, dnsNginx *dnsInfo
		if cfOK {
			if xrayDomain != "" {
				dnsReality = &dnsInfo{Domain: xrayDomain}
				rec, err := cfFindAAAARecord(xrayDomain)
				if err != nil {
					dnsReality.Error = err.Error()
				} else {
					dnsReality.Record = rec
					if rec != nil {
						dnsReality.Synced = rec.Content == xrayIP
					}
				}
			}
			nginxDomain := getEnv("XHTTP_DOMAIN")
			if nginxDomain != "" {
				dnsNginx = &dnsInfo{Domain: nginxDomain}
				rec, err := cfFindAAAARecord(nginxDomain)
				if err != nil {
					dnsNginx.Error = err.Error()
				} else {
					dnsNginx.Record = rec
					if rec != nil {
						dnsNginx.Synced = rec.Content == nginxIP
					}
				}
			}
		}

		writeJSON(w, map[string]any{
			"subnet":         prefix + "/64",
			"static_range":   staticRange,
			"rotating_range": rotatingRange,
			"nginx_ip":       nginxIP,
			"xray_ip":        xrayIP,
			"cf_configured":  cfOK,
			"dns_reality":    dnsReality,
			"dns_nginx":      dnsNginx,
		})
	}
}

func apiRotateIPv6(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subnet := getEnv("IPV6_SUBNET")
		primaryIP := getEnv("XRAY_IPV6")
		nginxIP := getEnv("NGINX_IPV6")
		domain := getEnv("XRAY_REALITY_DOMAIN")

		prefix, err := parseSubnetPrefix(subnet)
		if err != nil {
			writeError(w, "Invalid IPV6_SUBNET: "+err.Error(), 500)
			return
		}
		newIP, err := genRandomIPv6(prefix, primaryIP, nginxIP)
		if err != nil {
			writeError(w, "Failed to generate IPv6: "+err.Error(), 500)
			return
		}

		// Find xray container
		containers, err := cli.ContainerList(r.Context(), container.ListOptions{
			Filters: filters.NewArgs(filters.KeyValuePair{
				Key: "label", Value: "com.docker.compose.service=xray",
			}),
		})
		if err != nil || len(containers) == 0 {
			writeError(w, "Xray container not found", 500)
			return
		}
		xrayID := containers[0].ID

		// Detect the ipvlan interface (the one with IPv6 enabled)
		iface := ""
		for _, try := range []string{"eth1", "eth0", "eth2"} {
			var buf bytes.Buffer
			execConfig := container.ExecOptions{
				Cmd:          []string{"sh", "-c", fmt.Sprintf("cat /proc/sys/net/ipv6/conf/%s/disable_ipv6 2>/dev/null || echo 1", try)},
				AttachStdout: true,
				AttachStderr: true,
			}
			execResp, createErr := cli.ContainerExecCreate(r.Context(), xrayID, execConfig)
			if createErr == nil {
				attachResp, attachErr := cli.ContainerExecAttach(r.Context(), execResp.ID, container.ExecStartOptions{})
				if attachErr == nil {
					stdcopy.StdCopy(&buf, io.Discard, attachResp.Reader)
					attachResp.Close()
					val := strings.TrimSpace(buf.String())
					if val == "0" {
						iface = try
						break
					}
				}
			}
		}
		if iface == "" {
			iface = "eth1" // fallback
		}

		// Add new IPv6 to xray container on the ipvlan interface
		if err := containerExec(r.Context(), cli, xrayID, "ip", "addr", "add", newIP+"/64", "dev", iface); err != nil {
			writeError(w, "Failed to add IPv6 to xray: "+err.Error(), 500)
			return
		}

		// Update Cloudflare DNS
		if domain != "" && getEnv("CF_API_TOKEN") != "" {
			if _, err := cfUpsertAAAARecord(domain, newIP); err != nil {
				// Rollback: remove the IP we just added
				containerExec(r.Context(), cli, xrayID, "ip", "addr", "del", newIP+"/64", "dev", iface)
				writeError(w, "DNS update failed: "+err.Error(), 500)
				return
			}
		}

		// Remove old rotating IP
		oldState := loadRotatingState()
		if oldState.CurrentIP != "" && oldState.CurrentIP != primaryIP {
			containerExec(r.Context(), cli, xrayID, "ip", "addr", "del", oldState.CurrentIP+"/64", "dev", iface)
		}

		saveRotatingState(rotatingIPState{CurrentIP: newIP})

		writeJSON(w, map[string]any{
			"new_ip":  newIP,
			"domain":  domain,
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
		excluded := false
		for _, e := range exclude {
			if e != "" && ip == e {
				excluded = true
				break
			}
		}
		if !excluded {
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
		var results []string
		// Sync reality domain
		realityDomain := getEnv("XRAY_REALITY_DOMAIN")
		if realityDomain != "" {
			state := loadRotatingState()
			activeIP := state.CurrentIP
			if activeIP == "" {
				activeIP = getEnv("XRAY_IPV6")
			}
			if _, err := cfUpsertAAAARecord(realityDomain, activeIP); err != nil {
				results = append(results, fmt.Sprintf("reality: %s", err.Error()))
			} else {
				results = append(results, fmt.Sprintf("reality: %s → %s synced", realityDomain, activeIP))
			}
		}
		// Sync nginx domain
		nginxDomain := getEnv("XHTTP_DOMAIN")
		nginxIP := getEnv("NGINX_IPV6")
		if nginxDomain != "" && nginxIP != "" {
			if _, err := cfUpsertAAAARecord(nginxDomain, nginxIP); err != nil {
				results = append(results, fmt.Sprintf("nginx: %s", err.Error()))
			} else {
				results = append(results, fmt.Sprintf("nginx: %s → %s synced", nginxDomain, nginxIP))
			}
		}
		writeJSON(w, map[string]any{"results": results})
	}
}

func restartXray(cli *client.Client) {
	ctx := context.Background()
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{
			Key: "label", Value: "com.docker.compose.service=xray",
		}),
	})
	if err != nil || len(containers) == 0 {
		return
	}
	t := 10
	cli.ContainerRestart(ctx, containers[0].ID, container.StopOptions{Timeout: &t})
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
