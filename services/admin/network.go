package main

import (
	"admin/internal/platform"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"io"
	"log"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const rotatingIPv6CleanupGrace = 10 * time.Minute

// restart/轮转类长流程（stop+start+健康等待）的整体收敛时限。
const restartTimeout = 2 * time.Minute

var ipv6RotateMu sync.RWMutex

// -- Network / IPv6 rotation APIs --

// loadCurrentIPs 是 loadCurrentIPsWithRecords 的兼容包装（不需要记录明细的
// 调用方继续用两返回值签名）。
func loadCurrentIPs() (xrayIP, nginxIP string) {
	x, n, _ := loadCurrentIPsWithRecords()
	return x, n
}

// loadCurrentIPsWithRecords 除返回各容器当前 IPv6 外，还带出本次轮询查到的
// AAAA 记录明细，供 apiNetworkStatus 的 DNS 表格复用——同一请求内不再对
// 同一批域名发起第二轮全量 CF 查询（此前单次页面刷新 = 域数 × 2 轮串行 CF API）。
func loadCurrentIPsWithRecords() (xrayIP, nginxIP string, records map[string]*cfDNSRecord) {
	// env 回退值过 platform.NormalizeIPv6：CF 路径与 platform.GetContainerIPs 返回的都是压缩
	// 规范形，env 若填了全展开形式，ip_consistency 的字符串比较会永假，
	// 每 3 分钟误报一次 "missing rotating IP"（日志噪音 + no-op add）。
	xrayIP = platform.NormalizeIPv6(platform.GetEnv("XRAY_IPV6"))
	nginxIP = platform.NormalizeIPv6(platform.GetEnv("NGINX_IPV6"))
	records = map[string]*cfDNSRecord{}
	if platform.GetEnv("CF_API_TOKEN") == "" {
		return
	}
	for _, di := range allDomainInfos() {
		rec, err := cfFindAAAARecord(di.Domain)
		if err != nil || rec == nil {
			continue
		}
		records[di.Domain] = rec
		if di.Container == "xray" {
			xrayIP = platform.NormalizeIPv6(rec.Content)
		} else {
			nginxIP = platform.NormalizeIPv6(rec.Content)
		}
	}
	return
}

func apiNetworkStatus(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 整体收敛 deadline：此 handler 串行发起多轮 CF API + docker exec，
		// 全部挂在 r.Context() 上（客户端不断开就不取消），CF 慢时页面可挂起
		// 数分钟。20s 足够正常路径完成，异常时快速失败。
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		cfOK := platform.GetEnv("CF_API_TOKEN") != ""
		subnet := platform.GetEnv("IPV6_SUBNET")
		nginxStatic := platform.GetEnv("NGINX_IPV6")
		xrayStatic := platform.GetEnv("XRAY_IPV6")
		dnsXrayIP, dnsNginxIP, dnsRecords := loadCurrentIPsWithRecords()
		xrayIP, nxIP := dnsXrayIP, dnsNginxIP
		if xrayID, err := platform.FindContainer(r.Context(), cli, "xray"); err == nil {
			xrayIP = selectContainerDNSIP(r.Context(), cli, xrayID, xrayStatic, dnsXrayIP)
		}
		if nginxID, err := platform.FindContainer(r.Context(), cli, "nginx"); err == nil {
			nxIP = selectContainerDNSIP(r.Context(), cli, nginxID, nginxStatic, dnsNginxIP)
		}

		// Derive allocation ranges from subnet's first /64
		subnetDisplay := ""
		prefix := ""
		staticRange := ""
		rotatingRange := ""
		if p, err := netip.ParsePrefix(subnet); err == nil && p.Addr().Is6() && !p.Addr().Is4In6() {
			p = p.Masked()
			ip := p.Addr().As16()
			parts := make([]string, 4)
			for i := 0; i < 4; i++ {
				parts[i] = fmt.Sprintf("%x", binary.BigEndian.Uint16(ip[i*2:i*2+2]))
			}
			prefix = strings.Join(parts, ":") + ":"
			subnetDisplay = strings.TrimSuffix(prefix, ":") + "::/64"
			staticRange = prefix + "1::/80"
			rotatingRange = prefix + "f::/80"
		}

		// CF DNS records for all domains
		type dnsInfo struct {
			Domain     string       `json:"domain"`
			Container  string       `json:"container"`
			ExpectedIP string       `json:"expected_ip"`
			Record     *cfDNSRecord `json:"record"`
			Error      string       `json:"error,omitempty"`
			Synced     bool         `json:"synced"`
		}
		var dnsResults []dnsInfo
		if cfOK {
			for _, di := range allDomainInfos() {
				expectedIP := nxIP
				if di.Container == "xray" {
					expectedIP = xrayIP
				}
				info := dnsInfo{Domain: di.Domain, Container: di.Container, ExpectedIP: expectedIP}
				// 复用 loadCurrentIPsWithRecords 本轮已查到的记录：不再对同一
				// 批域名发起第二轮串行 CF 查询。查不到 = 记录缺失（rec=nil，
				// 展示为 mismatch），仅当本轮完全没查过（map 无键且非失败缓存）
				// 时才兜底补查。
				rec := dnsRecords[di.Domain]
				info.Record = rec
				if rec != nil {
					// 两端都归一化后再比较：CF 返回的 IPv6 压缩形式可能与
					// 预期值不同，直接字符串比较会误报 mismatch。
					info.Synced = platform.NormalizeIPv6(rec.Content) == platform.NormalizeIPv6(expectedIP)
				}
				dnsResults = append(dnsResults, info)
			}
		}

		platform.WriteJSON(w, map[string]any{
			"subnet":         subnetDisplay,
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

type domainInfo struct {
	Domain    string
	Container string
}

// allDomainInfos returns all managed domains and their container assignments.
// XRAY_REALITY_DOMAIN -> xray, NGINX_DOMAINS (comma-separated) -> nginx.
func allDomainInfos() []domainInfo {
	var infos []domainInfo
	if d := platform.GetEnv("XRAY_REALITY_DOMAIN"); d != "" {
		infos = append(infos, domainInfo{Domain: d, Container: "xray"})
	}
	if d := platform.GetEnv("XHTTP_DOMAIN"); d != "" {
		infos = append(infos, domainInfo{Domain: d, Container: "nginx"})
	}
	for _, d := range strings.Split(platform.GetEnv("NGINX_DOMAINS"), ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			infos = append(infos, domainInfo{Domain: d, Container: "nginx"})
		}
	}
	return infos
}

type dnsRollbackEntry struct {
	domain   string
	previous *cfDNSRecord
}

func cloneDNSRecord(rec *cfDNSRecord) *cfDNSRecord {
	if rec == nil {
		return nil
	}
	clone := *rec
	return &clone
}

func domainTargetIP(di domainInfo, xrayIP, nginxIP string) string {
	if di.Container == "xray" {
		return xrayIP
	}
	return nginxIP
}

func updateDomainAAAARecords(domains []domainInfo, xrayIP, nginxIP string) ([]string, bool, error) {
	var updated []string
	var rollback []dnsRollbackEntry
	for _, di := range domains {
		previous, err := cfFindAAAARecord(di.Domain)
		if err != nil {
			rollbackOK, rollbackErr := rollbackDomainAAAARecords(rollback)
			if rollbackErr != nil {
				return updated, rollbackOK, fmt.Errorf("read DNS record for %s: %w; rollback failed: %v", di.Domain, err, rollbackErr)
			}
			return updated, rollbackOK, fmt.Errorf("read DNS record for %s: %w", di.Domain, err)
		}
		ip := domainTargetIP(di, xrayIP, nginxIP)
		if _, err := cfUpsertAAAARecord(di.Domain, ip); err != nil {
			rollbackOK, rollbackErr := rollbackDomainAAAARecords(rollback)
			if rollbackErr != nil {
				return updated, rollbackOK, fmt.Errorf("update DNS record for %s: %w; rollback failed: %v", di.Domain, err, rollbackErr)
			}
			return updated, rollbackOK, fmt.Errorf("update DNS record for %s: %w", di.Domain, err)
		}
		rollback = append(rollback, dnsRollbackEntry{domain: di.Domain, previous: cloneDNSRecord(previous)})
		updated = append(updated, fmt.Sprintf("%s → %s", di.Domain, ip))
	}
	return updated, true, nil
}

func rollbackDomainAAAARecords(entries []dnsRollbackEntry) (bool, error) {
	var errs []string
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		var err error
		if entry.previous == nil {
			err = cfDeleteAAAARecord(entry.domain)
		} else {
			_, err = cfUpsertAAAARecord(entry.domain, entry.previous.Content)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", entry.domain, err))
		}
	}
	if len(errs) > 0 {
		return false, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return true, nil
}

// startupRotateIPv6 restores the DNS-published rotating IPv6 addresses on startup.
// It only allocates fresh addresses when DNS/current container state has no usable
// rotating address, avoiding unnecessary DNS churn on admin restarts.
func startupRotateIPv6(cli *client.Client) {
	if platform.GetEnv("CF_API_TOKEN") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 先等容器就绪——这段等待最长可达 3 分钟,绝不能持锁:轮换锁是全局互斥的,
	// 启动窗口内任何用户请求(xray 客户端增删、服务重启)都会在
	// restoreServiceRotatingIP 的 RLock 上挂起同样长的时间。
	var xrayID, nginxID string
	for {
		var xrayErr, nginxErr error
		xrayID, xrayErr = platform.FindContainer(ctx, cli, "xray")
		nginxID, nginxErr = platform.FindContainer(ctx, cli, "nginx")
		if xrayErr == nil && nginxErr == nil {
			break
		}
		if ctx.Err() != nil {
			log.Printf("startup rotate: waiting for containers: xray=%v nginx=%v", xrayErr, nginxErr)
			return
		}
		time.Sleep(2 * time.Second)
	}

	// 仅在真正分配 IP + 写 DNS 的临界区持锁;若手动轮换 API 正在执行,
	// 放弃本轮(ipConsistencyChecker 会兜底恢复),避免与其并发。
	if !ipv6RotateMu.TryLock() {
		log.Printf("startup rotate: rotation lock busy, skipping startup restore")
		return
	}
	defer ipv6RotateMu.Unlock()

	subnet := platform.GetEnv("IPV6_SUBNET")
	prefix, err := parseSubnetPrefix(subnet)
	if err != nil {
		log.Printf("startup IPv6 restore: subnet: %v", err)
		return
	}
	rotatingPrefix, err := rotatingIPv6Prefix(subnet)
	if err != nil {
		log.Printf("startup IPv6 restore: rotating prefix: %v", err)
		return
	}
	xrayStatic := platform.GetEnv("XRAY_IPV6")
	nginxStatic := platform.GetEnv("NGINX_IPV6")

	xrayAssigned := platform.GetContainerIPs(ctx, cli, xrayID)
	nginxAssigned := platform.GetContainerIPs(ctx, cli, nginxID)
	allAssigned := append(append([]string{}, xrayAssigned...), nginxAssigned...)
	dnsXrayIP, dnsNginxIP := loadCurrentIPs()

	xraySelection, err := selectStartupRotatingIP(
		prefix,
		rotatingPrefix,
		xrayStatic,
		dnsXrayIP,
		xrayAssigned,
		append([]string{xrayStatic, nginxStatic}, nginxAssigned...),
		append([]string{xrayStatic, nginxStatic}, allAssigned...),
	)
	if err != nil {
		log.Printf("startup IPv6 restore: xray IP: %v", err)
		return
	}
	xrayIP := xraySelection.IP

	nginxSelection, err := selectStartupRotatingIP(
		prefix,
		rotatingPrefix,
		nginxStatic,
		dnsNginxIP,
		nginxAssigned,
		append([]string{xrayStatic, nginxStatic, xrayIP}, xrayAssigned...),
		append([]string{xrayStatic, nginxStatic, xrayIP}, allAssigned...),
	)
	if err != nil {
		log.Printf("startup IPv6 restore: nginx IP: %v", err)
		return
	}
	nginxIP := nginxSelection.IP

	if err := platform.AddContainerIP(ctx, cli, xrayID, xrayIP); err != nil {
		log.Printf("startup IPv6 restore: add xray IP: %v", err)
		return
	}
	if err := platform.AddContainerIP(ctx, cli, nginxID, nginxIP); err != nil {
		if xraySelection.Generated {
			platform.DelContainerIP(ctx, cli, xrayID, xrayIP)
		}
		log.Printf("startup IPv6 restore: add nginx IP: %v", err)
		return
	}

	if _, rollbackOK, err := updateDomainAAAARecords(allDomainInfos(), xrayIP, nginxIP); err != nil {
		if rollbackOK {
			if xraySelection.Generated {
				platform.DelContainerIP(ctx, cli, xrayID, xrayIP)
			}
			if nginxSelection.Generated {
				platform.DelContainerIP(ctx, cli, nginxID, nginxIP)
			}
			log.Printf("startup IPv6 restore: DNS update failed: %v; rolled back newly generated IPs", err)
		} else {
			saveCurrentIPs(xrayIP, nginxIP)
			log.Printf("startup IPv6 restore: DNS update failed: %v; keeping restored IPs to avoid stale DNS", err)
		}
		return
	}

	saveCurrentIPs(xrayIP, nginxIP)
	retireStaleAssignedRotatingIPs("xray", xrayAssigned, xrayIP, rotatingPrefix)
	retireStaleAssignedRotatingIPs("nginx", nginxAssigned, nginxIP, rotatingPrefix)

	log.Printf("Startup IPv6 restore: xray=%s nginx=%s", xrayIP, nginxIP)
}

func apiRotateIPv6(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ipv6RotateMu.Lock()
		defer ipv6RotateMu.Unlock()

		subnet := platform.GetEnv("IPV6_SUBNET")
		xrayStatic := platform.GetEnv("XRAY_IPV6")
		nginxStatic := platform.GetEnv("NGINX_IPV6")

		if platform.GetEnv("CF_API_TOKEN") == "" {
			platform.WriteError(w, "CF_API_TOKEN not configured", 400)
			return
		}

		// 使用独立 context 避免 HTTP 客户端断开取消长时间运行的轮换操作。
		// 轮换涉及多次 Docker API + DNS 更新，客户端断开可能导致部分更新。
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		xrayID, err := platform.FindContainer(ctx, cli, "xray")
		if err != nil {
			// 服务暂时不可用（容器缺失/查询失败）而非内部错误：与 apiSyncDNS
			// 同场景的写法对齐（platform.WriteInternalError 记日志 + 通用文案），503
			// 让客户端明确这是环境态而非代码 bug。
			log.Printf("rotate ipv6: find xray container: %v", err)
			platform.WriteError(w, "Xray container not available", http.StatusServiceUnavailable)
			return
		}
		nginxID, err := platform.FindContainer(ctx, cli, "nginx")
		if err != nil {
			platform.WriteError(w, "Nginx container not found", 500)
			return
		}

		// Collect assigned IPs to avoid collisions; generate separate IPs per container
		prefix, err := parseSubnetPrefix(subnet)
		if err != nil {
			log.Printf("Invalid IPV6_SUBNET: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		rotatingPrefix, err := rotatingIPv6Prefix(subnet)
		if err != nil {
			log.Printf("Invalid IPV6_SUBNET rotating prefix: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		xrayAssigned := platform.GetContainerIPs(ctx, cli, xrayID)
		nginxAssigned := platform.GetContainerIPs(ctx, cli, nginxID)
		allAssigned := append(append([]string{}, xrayAssigned...), nginxAssigned...)
		xrayExclude := append([]string{xrayStatic, nginxStatic}, allAssigned...)
		xrayIP, err := genRandomIPv6(prefix, xrayExclude...)
		if err != nil {
			log.Printf("Failed to generate xray IPv6: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		nginxExclude := append([]string{xrayStatic, nginxStatic, xrayIP}, allAssigned...)
		nginxIP, err := genRandomIPv6(prefix, nginxExclude...)
		if err != nil {
			log.Printf("Failed to generate nginx IPv6: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		oldXrayIP, oldNginxIP := loadCurrentIPs()

		// Add new IPs
		if err := platform.AddContainerIP(ctx, cli, xrayID, xrayIP); err != nil {
			log.Printf("Failed to add IPv6 to xray: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		if err := platform.AddContainerIP(ctx, cli, nginxID, nginxIP); err != nil {
			platform.DelContainerIP(ctx, cli, xrayID, xrayIP)
			log.Printf("Failed to add IPv6 to nginx: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		// Update DNS per domain → container mapping
		domains := allDomainInfos()
		updated, rollbackOK, err := updateDomainAAAARecords(domains, xrayIP, nginxIP)
		if err != nil {
			if rollbackOK {
				platform.DelContainerIP(ctx, cli, xrayID, xrayIP)
				platform.DelContainerIP(ctx, cli, nginxID, nginxIP)
				log.Printf("DNS update failed: %v; rolled back DNS and IPs", err)
			} else {
				saveCurrentIPs(xrayIP, nginxIP)
				log.Printf("DNS update failed: %v; keeping new IPs to avoid stale DNS", err)
			}
			platform.WriteError(w, "DNS update failed", 500)
			return
		}

		saveCurrentIPs(xrayIP, nginxIP)
		var retired []retiredIPv6Entry
		if oldXrayIP != "" && platform.NormalizeIPv6(oldXrayIP) != platform.NormalizeIPv6(xrayStatic) && isRotatingIPv6InPrefix(oldXrayIP, rotatingPrefix) {
			retired = append(retired, retiredIPv6Entry{Service: "xray", IP: oldXrayIP})
		}
		if oldNginxIP != "" && platform.NormalizeIPv6(oldNginxIP) != platform.NormalizeIPv6(nginxStatic) && isRotatingIPv6InPrefix(oldNginxIP, rotatingPrefix) {
			retired = append(retired, retiredIPv6Entry{Service: "nginx", IP: oldNginxIP})
		}
		retireRotatingIPs(retired...)
		retireStaleAssignedRotatingIPs("xray", xrayAssigned, xrayIP, rotatingPrefix)
		retireStaleAssignedRotatingIPs("nginx", nginxAssigned, nginxIP, rotatingPrefix)

		platform.WriteJSON(w, map[string]any{
			"xray_ip":               xrayIP,
			"nginx_ip":              nginxIP,
			"domains":               updated,
			"cleanup_grace_seconds": int(rotatingIPv6CleanupGrace.Seconds()),
		})
	}
}

func parseSubnetPrefix(subnet string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return "", fmt.Errorf("parse subnet: %w", err)
	}
	prefix = prefix.Masked()
	// Return the first 64 bits as hex string with trailing ::
	addr := prefix.Addr()
	if !addr.Is6() || addr.Is4In6() {
		return "", fmt.Errorf("not an IPv6 subnet")
	}
	// Build prefix like "2605:a141:2322:2623:"
	ip := addr.As16()
	parts := make([]string, 4)
	for i := 0; i < 4; i++ {
		parts[i] = fmt.Sprintf("%x", binary.BigEndian.Uint16(ip[i*2:i*2+2]))
	}
	return strings.Join(parts, ":") + ":", nil
}

func genRandomIPv6(prefix string, exclude ...string) (string, error) {
	excludeSet := map[string]bool{}
	for _, e := range exclude {
		if normalized := platform.NormalizeIPv6(e); normalized != "" {
			excludeSet[normalized] = true
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
		ip = platform.NormalizeIPv6(ip)
		if !excludeSet[ip] {
			return ip, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique IPv6 after 100 attempts")
}

func apiSyncDNS(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ipv6RotateMu.Lock()
		defer ipv6RotateMu.Unlock()
		if platform.GetEnv("CF_API_TOKEN") == "" {
			platform.WriteError(w, "CF_API_TOKEN not configured", 400)
			return
		}
		// 使用独立 context 避免 HTTP 客户端断开取消 DNS 同步操作。
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		// Get actual container IPs — no local state, containers are the source of truth.
		xrayID, err := platform.FindContainer(ctx, cli, "xray")
		if err != nil {
			log.Printf("xray container: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		nginxID, err := platform.FindContainer(ctx, cli, "nginx")
		if err != nil {
			log.Printf("nginx container: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		oldXrayIP, oldNginxIP := loadCurrentIPs()
		xrayIP := selectContainerDNSIP(ctx, cli, xrayID, platform.GetEnv("XRAY_IPV6"), oldXrayIP)
		nginxIP := selectContainerDNSIP(ctx, cli, nginxID, platform.GetEnv("NGINX_IPV6"), oldNginxIP)

		var results []string
		for _, di := range allDomainInfos() {
			ip := nginxIP
			if di.Container == "xray" {
				ip = xrayIP
			}
			if _, err := cfUpsertAAAARecord(di.Domain, ip); err != nil {
				// CF 错误详情只进日志,客户端只看到通用文案。
				log.Printf("sync-dns: upsert %s: %v", di.Domain, err)
				results = append(results, fmt.Sprintf("%s: DNS 更新失败", di.Domain))
			} else {
				results = append(results, fmt.Sprintf("%s → %s synced", di.Domain, ip))
			}
		}
		saveCurrentIPs(xrayIP, nginxIP)
		platform.WriteJSON(w, map[string]any{"results": results})
	}
}

// selectContainerDNSIP returns the best public IPv6 target for DNS:
// keep the current rotating DNS IP if it is still assigned, otherwise prefer
// any assigned rotating-range IP over the static Docker-assigned address.
func selectContainerDNSIP(ctx context.Context, cli *client.Client, containerID, staticIP, currentDNSIP string) string {
	ips := platform.GetContainerIPs(ctx, cli, containerID)
	if len(ips) == 0 {
		return staticIP
	}
	subnet := platform.GetEnv("IPV6_SUBNET")
	rotatingPrefix, err := rotatingIPv6Prefix(subnet)
	if err != nil {
		return staticIP
	}
	currentDNSIP = platform.NormalizeIPv6(currentDNSIP)
	staticIP = platform.NormalizeIPv6(staticIP)
	if currentDNSIP != "" && currentDNSIP != staticIP && isRotatingIPv6InPrefix(currentDNSIP, rotatingPrefix) {
		for _, ip := range ips {
			if platform.NormalizeIPv6(ip) == currentDNSIP {
				return currentDNSIP
			}
		}
	}
	for _, ip := range ips {
		if isRotatingIPv6InPrefix(ip, rotatingPrefix) {
			return platform.NormalizeIPv6(ip)
		}
	}
	for _, ip := range ips {
		if platform.NormalizeIPv6(ip) == staticIP {
			return ip
		}
	}
	return staticIP
}

func restartXray(ctx context.Context, cli *client.Client) error {
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		Filters: platform.ComposeProjectFilters(filters.KeyValuePair{
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

	t := platform.DockerStopTimeoutSeconds
	restartErr := cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &t})

	// 始终尝试恢复轮换 IP。Docker restart 超时/cancel 时容器可能已完成重启，
	// 不恢复会导致 DNS 指向不存在的地址。
	restoreServiceRotatingIP(ctx, cli, id, "xray")

	if restartErr != nil {
		return fmt.Errorf("restart xray: %w", restartErr)
	}
	return nil
}

func restoreServiceRotatingIP(ctx context.Context, cli *client.Client, containerID, service string) {
	if platform.GetEnv("CF_API_TOKEN") == "" {
		return
	}
	// 非阻塞获取读锁:启动轮换/手动轮换持写锁期间跳过本轮恢复(与
	// checkIPConsistency 的 TryRLock 一致),避免请求挂起在锁上。
	if !ipv6RotateMu.TryRLock() {
		return
	}
	defer ipv6RotateMu.RUnlock()
	xrayIP, nginxIP := loadCurrentIPs()
	ip := xrayIP
	if service == "nginx" {
		ip = nginxIP
	}
	if ip == "" {
		return
	}
	if err := platform.AddContainerIP(ctx, cli, containerID, ip); err != nil {
		log.Printf("WARNING: failed to restore %s rotating IPv6 %s: %v", service, ip, err)
	}
}
