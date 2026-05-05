package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

func getNginxContainerID(cli *client.Client) (string, error) {
	containers, err := cli.ContainerList(context.Background(), container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{
			Key: "label", Value: "com.docker.compose.service=nginx",
		}),
	})
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", fmt.Errorf("nginx container not found")
	}
	return containers[0].ID, nil
}

func fail2banClient(cli *client.Client, args ...string) (string, error) {
	cid, err := getNginxContainerID(cli)
	if err != nil {
		return "", err
	}

	cmd := append([]string{"fail2ban-client"}, args...)
	ctx := context.Background()

	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := cli.ContainerExecCreate(ctx, cid, execConfig)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}

	attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer attachResp.Close()

	var stdoutBuf, stderrBuf strings.Builder
	stdcopy.StdCopy(io.Writer(&stdoutBuf), io.Writer(&stderrBuf), attachResp.Reader)

	inspect, err := cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return "", fmt.Errorf("exec inspect: %w", err)
	}
	if inspect.ExitCode != 0 {
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(stdoutBuf.String())
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("exit code %d", inspect.ExitCode)
		}
		return "", fmt.Errorf("fail2ban-client %v: %s", args, errMsg)
	}

	return strings.TrimSpace(stdoutBuf.String()), nil
}

type jailInfo struct {
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	CurrentlyFailed int      `json:"currently_failed"`
	TotalFailed     int      `json:"total_failed"`
	CurrentlyBanned int      `json:"currently_banned"`
	TotalBanned     int      `json:"total_banned"`
	BannedIPs       []string `json:"banned_ips"`
	Filter          string   `json:"filter"`
	LogPath         string   `json:"logpath"`
	BanTime         int      `json:"bantime"`
	FindTime        int      `json:"findtime"`
	MaxRetry        int      `json:"maxretry"`
}

func parseFail2banStatus(cli *client.Client, output string) ([]jailInfo, error) {
	lines := strings.Split(output, "\n")
	var jailNames []string
	for _, line := range lines {
		if idx := strings.Index(line, "Jail list:"); idx >= 0 {
			parts := strings.SplitN(line[idx:], ":", 2)
			if len(parts) == 2 {
				for _, name := range strings.Split(parts[1], ",") {
					name = strings.TrimSpace(name)
					if name != "" {
						jailNames = append(jailNames, name)
					}
				}
			}
		}
	}

	var jails []jailInfo
	for _, name := range jailNames {
		jail := jailInfo{Name: name, Enabled: true}

		detail, err := fail2banClient(cli, "status", name)
		if err != nil {
			jails = append(jails, jail)
			continue
		}

		// Parse nested status output:
		// Status for the jail: foo
		// |- Filter
		// |  |- Currently failed: 0
		// |  |- Total failed:     0
		// |  `- File list:        /path
		// `- Actions
		//    |- Currently banned:  0
		//    |- Total banned:      0
		//    `- Banned IP list:
		for _, line := range strings.Split(detail, "\n") {
			line = strings.TrimSpace(line)
			// Strip leading tree characters like "|  |- ", "|  `- ", "|- ", "`- "
			content := stripTreePrefix(line)

			switch {
			case strings.HasPrefix(content, "Currently failed:"):
				fmt.Sscanf(content, "Currently failed:%d", &jail.CurrentlyFailed)
			case strings.HasPrefix(content, "Total failed:"):
				fmt.Sscanf(content, "Total failed:%d", &jail.TotalFailed)
			case strings.HasPrefix(content, "Currently banned:"):
				fmt.Sscanf(content, "Currently banned:%d", &jail.CurrentlyBanned)
			case strings.HasPrefix(content, "Total banned:"):
				fmt.Sscanf(content, "Total banned:%d", &jail.TotalBanned)
			case strings.HasPrefix(content, "File list:"):
				jail.LogPath = strings.TrimSpace(content[len("File list:"):])
			case strings.HasPrefix(content, "Banned IP list:"):
				rest := strings.TrimSpace(content[len("Banned IP list:"):])
				if rest != "" {
					jail.BannedIPs = strings.Fields(rest)
				}
			}
		}

		// Get filter name and config via get-commands
		if filterName, err := fail2banClient(cli, "get", name, "filter"); err == nil {
			jail.Filter = strings.TrimSpace(filterName)
		}
		if bt, err := fail2banClient(cli, "get", name, "bantime"); err == nil {
			fmt.Sscanf(strings.TrimSpace(bt), "%d", &jail.BanTime)
		}
		if ft, err := fail2banClient(cli, "get", name, "findtime"); err == nil {
			fmt.Sscanf(strings.TrimSpace(ft), "%d", &jail.FindTime)
		}
		if mr, err := fail2banClient(cli, "get", name, "maxretry"); err == nil {
			fmt.Sscanf(strings.TrimSpace(mr), "%d", &jail.MaxRetry)
		}

		jails = append(jails, jail)
	}
	return jails, nil
}

func stripTreePrefix(line string) string {
	// Remove tree-drawing characters like "|  |- ", "`- ", etc.
	for i, r := range line {
		if r != ' ' && r != '|' && r != '`' && r != '-' {
			return line[i:]
		}
	}
	return line
}

func apiJails(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := fail2banClient(cli, "status")
		if err != nil {
			writeError(w, "fail2ban-client failed: "+err.Error(), 500)
			return
		}
		jails, err := parseFail2banStatus(cli, status)
		if err != nil {
			writeError(w, "Failed to parse fail2ban status: "+err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"jails": jails})
	}
}

func apiBanIP(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ IP string `json:"ip"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
			writeError(w, "IP required", 400)
			return
		}
		if _, err := fail2banClient(cli, "set", "admin-manual", "banip", body.IP); err != nil {
			writeError(w, "Failed to ban IP: "+err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiUnbanIP(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ IP string `json:"ip"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
			writeError(w, "IP required", 400)
			return
		}
		if _, err := fail2banClient(cli, "set", "admin-manual", "unbanip", body.IP); err != nil {
			writeError(w, "Failed to unban IP: "+err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}
