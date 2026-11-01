package proxy

import (
	"admin/internal/platform"
	"admin/internal/requestctx"
	"context"
	"fmt"
	"github.com/docker/docker/client"
	"log"
	"net/http"
	"time"
)

// StartupRotateIPv6 在启动时恢复 DNS 已发布的轮换 IPv6 地址。
// 仅当 DNS/容器当前状态没有可用地址时才分配新地址；无 CF token 时 no-op。
func StartupRotateIPv6(cli *client.Client) {
	if platform.GetEnv("CF_API_TOKEN") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 先等容器就绪——这段等待最长可达 3 分钟,绝不能持锁:轮换锁是全局互斥的,
	// 启动窗口内任何用户请求(xray 客户端增删、服务重启)都会在
	// RestoreServiceRotatingIP 的 RLock 上挂起同样长的时间。
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
	// 放弃本轮(IPConsistencyChecker 会兜底恢复),避免与其并发。
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
	dnsXrayIP, dnsNginxIP := LoadCurrentIPs(ctx)

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

	if _, rollbackOK, err := updateDomainAAAARecords(ctx, allDomainInfos(), xrayIP, nginxIP); err != nil {
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

func ApiRotateIPv6(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ipv6RotateMu.Lock()
		defer ipv6RotateMu.Unlock()

		subnet := platform.GetEnv("IPV6_SUBNET")
		xrayStatic := platform.GetEnv("XRAY_IPV6")
		nginxStatic := platform.GetEnv("NGINX_IPV6")

		if platform.GetEnv("CF_API_TOKEN") == "" {
			platform.WriteError(w, "CF_API_TOKEN 未配置", 400)
			return
		}

		// 使用独立 context 避免 HTTP 客户端断开取消长时间运行的轮换操作。
		// 轮换涉及多次 Docker API + DNS 更新，客户端断开可能导致部分更新。
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		xrayID, err := platform.FindContainer(ctx, cli, "xray")
		if err != nil {
			// 服务暂时不可用（容器缺失/查询失败）而非内部错误：与 ApiSyncDNS
			// 同场景的写法对齐（platform.WriteInternalError 记日志 + 通用文案），503
			// 让客户端明确这是环境态而非代码 bug。
			log.Printf("rotate ipv6: find xray container: %v", err)
			platform.WriteError(w, "Xray 容器不可用", http.StatusServiceUnavailable)
			return
		}
		nginxID, err := platform.FindContainer(ctx, cli, "nginx")
		if err != nil {
			platform.WriteError(w, "未找到 Nginx 容器", 500)
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

		oldXrayIP, oldNginxIP := LoadCurrentIPs(ctx)

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
		updated, rollbackOK, err := updateDomainAAAARecords(ctx, domains, xrayIP, nginxIP)
		if err != nil {
			if rollbackOK {
				platform.DelContainerIP(ctx, cli, xrayID, xrayIP)
				platform.DelContainerIP(ctx, cli, nginxID, nginxIP)
				log.Printf("DNS update failed: %v; rolled back DNS and IPs", err)
			} else {
				saveCurrentIPs(xrayIP, nginxIP)
				log.Printf("DNS update failed: %v; keeping new IPs to avoid stale DNS", err)
			}
			platform.WriteError(w, "DNS 更新失败", 500)
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

		actor, _ := requestctx.ActorFrom(r.Context())
		log.Printf("[security] rotate-ipv6 xray_ip=%s nginx_ip=%s domains=%d by=%s", xrayIP, nginxIP, len(updated), actor.Username)
		platform.WriteJSON(w, map[string]any{
			"xray_ip":               xrayIP,
			"nginx_ip":              nginxIP,
			"domains":               updated,
			"cleanup_grace_seconds": int(rotatingIPv6CleanupGrace.Seconds()),
		})
	}
}

func ApiSyncDNS(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ipv6RotateMu.Lock()
		defer ipv6RotateMu.Unlock()
		if platform.GetEnv("CF_API_TOKEN") == "" {
			platform.WriteError(w, "CF_API_TOKEN 未配置", 400)
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

		oldXrayIP, oldNginxIP := LoadCurrentIPs(ctx)
		xrayIP := selectContainerDNSIP(ctx, cli, xrayID, platform.GetEnv("XRAY_IPV6"), oldXrayIP)
		nginxIP := selectContainerDNSIP(ctx, cli, nginxID, platform.GetEnv("NGINX_IPV6"), oldNginxIP)

		var results []string
		failed := 0
		for _, di := range allDomainInfos() {
			ip := nginxIP
			if di.Container == "xray" {
				ip = xrayIP
			}
			if _, err := cfUpsertAAAARecord(ctx, di.Domain, ip); err != nil {
				// CF 错误详情只进日志,客户端只看到通用文案。
				log.Printf("sync-dns: upsert %s: %v", di.Domain, err)
				results = append(results, fmt.Sprintf("%s: DNS 更新失败", di.Domain))
				failed++
			} else {
				results = append(results, fmt.Sprintf("%s → %s synced", di.Domain, ip))
			}
		}
		saveCurrentIPs(xrayIP, nginxIP)
		actor, _ := requestctx.ActorFrom(r.Context())
		// 审计区分成败:全失败也记成功形态的行,审计线无法区分事故(对照
		// rotate-ipv6 只在真正成功后留痕)。
		log.Printf("[security] sync-dns ok=%d failed=%d xray_ip=%s nginx_ip=%s by=%s", len(results)-failed, failed, xrayIP, nginxIP, actor.Username)
		platform.WriteJSON(w, map[string]any{"results": results})
	}
}

// selectContainerDNSIP returns the best public IPv6 target for DNS:
// keep the current rotating DNS IP if it is still assigned, otherwise prefer
