package proxy

import (
	"admin/internal/platform"
	"context"
	"fmt"
	"github.com/docker/docker/client"
	"strings"
)

type domainInfo struct {
	Domain    string
	Container string
}

// allDomainInfos returns all managed domains and their container assignments.
// NGINX_DOMAINS (comma-separated) -> nginx, plus XHTTP_DOMAIN.
func allDomainInfos() []domainInfo {
	var infos []domainInfo
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

func updateDomainAAAARecords(ctx context.Context, domains []domainInfo, xrayIP, nginxIP string) ([]string, bool, error) {
	var updated []string
	var rollback []dnsRollbackEntry
	for _, di := range domains {
		previous, err := cfFindAAAARecord(ctx, di.Domain)
		if err != nil {
			rollbackOK, rollbackErr := rollbackDomainAAAARecords(ctx, rollback)
			if rollbackErr != nil {
				return updated, rollbackOK, fmt.Errorf("read DNS record for %s: %w; rollback failed: %v", di.Domain, err, rollbackErr)
			}
			return updated, rollbackOK, fmt.Errorf("read DNS record for %s: %w", di.Domain, err)
		}
		ip := domainTargetIP(di, xrayIP, nginxIP)
		if _, err := cfUpsertAAAARecord(ctx, di.Domain, ip); err != nil {
			rollbackOK, rollbackErr := rollbackDomainAAAARecords(ctx, rollback)
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

func rollbackDomainAAAARecords(ctx context.Context, entries []dnsRollbackEntry) (bool, error) {
	var errs []string
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		var err error
		if entry.previous == nil {
			err = cfDeleteAAAARecord(ctx, entry.domain)
		} else {
			_, err = cfUpsertAAAARecord(ctx, entry.domain, entry.previous.Content)
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
