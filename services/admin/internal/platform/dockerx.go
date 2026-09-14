package platform

import (
	"bytes"
	"context"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"io"
	"log"
	"net/netip"
	"strings"
)

// ipv6RotateMu 保护 IPv6 轮转操作，防止并发轮转。
// 写锁：startupRotateIPv6 / apiRotateIPv6（分配新 IP、更新 DNS）。
// 读锁：restoreServiceRotatingIP（恢复当前 IP，不修改 DNS）。
// docker 容器 stop 的宽限期（秒）：超时后 SIGKILL。service action / 部署
// 重启路径共用。
const DockerStopTimeoutSeconds = 10

// FindContainer returns the container ID for a given compose service label.
func FindContainer(ctx context.Context, cli *client.Client, service string) (string, error) {
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		Filters: ComposeProjectFilters(filters.KeyValuePair{
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

// AddContainerIP adds an IPv6 to the container's default-route interface.
func AddContainerIP(ctx context.Context, cli *client.Client, containerID, ip string) error {
	ip = NormalizeIPv6(ip)
	return ContainerExec(ctx, cli, containerID, "sh", "-c",
		fmt.Sprintf(`dev=$(ip -6 route show default | awk '{for (i=1;i<=NF;i++) if ($i=="dev") {print $(i+1); exit}}')
if [ -z "$dev" ]; then echo "default IPv6 interface not found" >&2; exit 1; fi
if ip -6 -o addr show dev "$dev" | awk '{print $4}' | cut -d/ -f1 | grep -Fxq %s; then exit 0; fi
ip addr add %s/64 dev "$dev"`, ShellQuote(ip), ShellQuote(ip)))
}

func DelContainerIP(ctx context.Context, cli *client.Client, containerID, ip string) error {
	ip = NormalizeIPv6(ip)
	return ContainerExec(ctx, cli, containerID, "sh", "-c",
		fmt.Sprintf(`dev=$(ip -6 route show default | awk '{for (i=1;i<=NF;i++) if ($i=="dev") {print $(i+1); exit}}')
if [ -z "$dev" ]; then echo "default IPv6 interface not found" >&2; exit 1; fi
if ! ip -6 -o addr show dev "$dev" | awk '{print $4}' | cut -d/ -f1 | grep -Fxq %s; then exit 0; fi
ip addr del %s/64 dev "$dev"`, ShellQuote(ip), ShellQuote(ip)))
}

// GetContainerIPs returns all global IPv6 addresses in our subnet from a container.
func GetContainerIPs(ctx context.Context, cli *client.Client, containerID string) []string {
	subnet := GetEnv("IPV6_SUBNET")
	ipv6Prefix, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil || !ipv6Prefix.Addr().Is6() || ipv6Prefix.Addr().Is4In6() {
		return nil
	}
	ipv6Prefix = ipv6Prefix.Masked()
	var buf bytes.Buffer
	execConfig := container.ExecOptions{
		Cmd:          []string{"sh", "-c", "ip -6 -o addr show 2>/dev/null | awk '{print $4}' | cut -d/ -f1"},
		AttachStdout: true, AttachStderr: true,
	}
	execResp, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		log.Printf("GetContainerIPs: exec create %s: %v", containerID[:12], err)
		return nil
	}
	attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		log.Printf("GetContainerIPs: exec attach %s: %v", containerID[:12], err)
		return nil
	}
	defer attachResp.Close()
	stdcopy.StdCopy(&buf, io.Discard, attachResp.Reader)
	var ips []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		ip := NormalizeIPv6(line)
		addr, err := netip.ParseAddr(ip)
		if err == nil && addr.Is6() && !addr.IsLoopback() && ipv6Prefix.Contains(addr) {
			ips = append(ips, addr.String())
		}
	}
	return ips
}

func ContainerExec(ctx context.Context, cli *client.Client, containerID string, cmd ...string) error {
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
