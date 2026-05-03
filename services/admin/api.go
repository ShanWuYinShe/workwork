package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"

	"admin/handlers"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
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
		writeJSON(w, map[string]any{"stats": stats, "services": svcs})
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
			"xrayVersion": "-", "xrayInbounds": 0, "xrayClients": 0,
			"goVersion": "-", "configStatus": "-",
		}
		if out, err := exec.Command("/usr/local/bin/xray", "version").Output(); err == nil {
			v := string(out)
			if len(v) > 80 {
				v = v[:80]
			}
			data["xrayVersion"] = v
		}
		if out, err := exec.Command("go", "version").Output(); err == nil {
			data["goVersion"] = string(out)
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
		if project == "" {
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
		xrayClients, _ := handlers.ReadXrayClients()
		genJSON := handlers.GenerateClientConfig(xrayClients)
		adminUsers := readHtpasswdUsers()
		writeJSON(w, map[string]any{
			"xrayClients": xrayClients, "generatedJSON": genJSON, "adminUsers": adminUsers,
		})
	}
}

func apiAddXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ UUID string `json:"uuid"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UUID == "" {
			writeError(w, "UUID required", 400)
			return
		}
		data, err := os.ReadFile("/app/configs/xray.json")
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		var full map[string]any
		json.Unmarshal(data, &full)
		inbounds, _ := full["inbounds"].([]any)
		for i := range inbounds {
			ib, _ := inbounds[i].(map[string]any)
			tag, _ := ib["tag"].(string)
			settings, _ := ib["settings"].(map[string]any)
			if settings == nil {
				continue
			}
			clientsRaw, _ := settings["clients"].([]any)
			nc := map[string]any{"id": body.UUID}
			if tag == "reality-in" {
				nc["flow"] = "xtls-rprx-vision"
			}
			clientsRaw = append(clientsRaw, nc)
			settings["clients"] = clientsRaw
			settings["decryption"] = "none"
		}
		out, _ := json.MarshalIndent(full, "", "  ")
		os.WriteFile("/app/configs/xray.json", out, 0644)
		go func() { exec.Command("/bin/sh", "/app/deploy.sh").Run() }()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiDeleteXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ UUID string `json:"uuid"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UUID == "" {
			writeError(w, "UUID required", 400)
			return
		}
		data, _ := os.ReadFile("/app/configs/xray.json")
		var full map[string]any
		json.Unmarshal(data, &full)
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
				if id, _ := cm["id"].(string); id != body.UUID {
					kept = append(kept, c)
				}
			}
			settings["clients"] = kept
		}
		out, _ := json.MarshalIndent(full, "", "  ")
		os.WriteFile("/app/configs/xray.json", out, 0644)
		go func() { exec.Command("/bin/sh", "/app/deploy.sh").Run() }()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiChangePassword(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			writeError(w, "Username and password required", 400)
			return
		}
		out, err := exec.Command("htpasswd", "-nbB", body.Username, body.Password).Output()
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		os.WriteFile("/app/htpasswd", out, 0644)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiAddAdmin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
			writeError(w, "Username and password required", 400)
			return
		}
		out, err := exec.Command("htpasswd", "-nbB", body.Username, body.Password).Output()
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		os.WriteFile("/app/htpasswd", out, 0644)
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
		data, _ := os.ReadFile("/app/htpasswd")
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, body.Username+":") {
				lines = append(lines, line)
			}
		}
		os.WriteFile("/app/htpasswd", []byte(strings.Join(lines, "\n")), 0644)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func readHtpasswdUsers() []string {
	data, err := os.ReadFile("/app/htpasswd")
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
