package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/client"
)

var (
	rotatingIPStatePath = "/app/data/rotating-ips.json"
	rotatingIPStateMu   sync.Mutex
)

type rotatingIPState struct {
	Current map[string]string  `json:"current"`
	Retired []retiredIPv6Entry `json:"retired,omitempty"`
}

type retiredIPv6Entry struct {
	Service      string    `json:"service"`
	IP           string    `json:"ip"`
	CleanupAfter time.Time `json:"cleanup_after"`
}

type startupIPv6Selection struct {
	IP        string
	Generated bool
}

func loadRotatingIPStateLocked() rotatingIPState {
	defaultState := rotatingIPState{Current: map[string]string{}}
	data, err := os.ReadFile(rotatingIPStatePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("WARNING: read rotating IP state: %v", err)
		}
		return defaultState
	}

	var state rotatingIPState
	if err := json.Unmarshal(data, &state); err == nil && (state.Current != nil || state.Retired != nil) {
		if state.Current == nil {
			state.Current = map[string]string{}
		}
		normalizeRotatingIPState(&state)
		return state
	}

	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err != nil {
		log.Printf("WARNING: parse rotating IP state: %v", err)
		return defaultState
	}
	state.Current = legacy
	normalizeRotatingIPState(&state)
	return state
}

func saveRotatingIPStateLocked(state rotatingIPState) {
	normalizeRotatingIPState(&state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("ERROR: marshal rotating IP state: %v", err)
		return
	}
	if err := atomicWriteFile(rotatingIPStatePath, data, 0600); err != nil {
		log.Printf("ERROR: save rotating IP state: %v", err)
	}
}

func normalizeRotatingIPState(state *rotatingIPState) {
	if state.Current == nil {
		state.Current = map[string]string{}
	}
	for service, ip := range state.Current {
		if normalized := normalizeIPv6(ip); normalized != "" {
			state.Current[service] = normalized
		}
	}

	dedup := make(map[string]retiredIPv6Entry)
	for _, entry := range state.Retired {
		entry.Service = strings.TrimSpace(entry.Service)
		entry.IP = normalizeIPv6(entry.IP)
		if entry.Service == "" || entry.IP == "" {
			continue
		}
		key := entry.Service + "\x00" + entry.IP
		if existing, ok := dedup[key]; !ok || entry.CleanupAfter.After(existing.CleanupAfter) {
			dedup[key] = entry
		}
	}
	state.Retired = state.Retired[:0]
	for _, entry := range dedup {
		state.Retired = append(state.Retired, entry)
	}
}

// saveCurrentIPs persists the active rotating IPs and the retired-IP cleanup queue.
func saveCurrentIPs(xrayIP, nginxIP string) {
	rotatingIPStateMu.Lock()
	defer rotatingIPStateMu.Unlock()

	state := loadRotatingIPStateLocked()
	if xrayIP != "" {
		state.Current["xray"] = normalizeIPv6(xrayIP)
	}
	if nginxIP != "" {
		state.Current["nginx"] = normalizeIPv6(nginxIP)
	}
	saveRotatingIPStateLocked(state)
}

func retireRotatingIPs(entries ...retiredIPv6Entry) {
	if len(entries) == 0 {
		return
	}

	rotatingIPStateMu.Lock()
	defer rotatingIPStateMu.Unlock()

	state := loadRotatingIPStateLocked()
	now := time.Now()
	for _, entry := range entries {
		entry.Service = strings.TrimSpace(entry.Service)
		entry.IP = normalizeIPv6(entry.IP)
		if entry.Service == "" || entry.IP == "" {
			continue
		}
		if state.Current[entry.Service] == entry.IP {
			continue
		}
		if entry.CleanupAfter.IsZero() {
			entry.CleanupAfter = now.Add(rotatingIPv6CleanupGrace)
		}
		state.Retired = append(state.Retired, entry)
	}
	saveRotatingIPStateLocked(state)
}

func retireStaleAssignedRotatingIPs(service string, assigned []string, keepIP string, rotatingPrefix netip.Prefix) {
	keepIP = normalizeIPv6(keepIP)
	var entries []retiredIPv6Entry
	for _, ip := range assigned {
		ip = normalizeIPv6(ip)
		if ip == "" || ip == keepIP || !isRotatingIPv6InPrefix(ip, rotatingPrefix) {
			continue
		}
		entries = append(entries, retiredIPv6Entry{Service: service, IP: ip})
	}
	retireRotatingIPs(entries...)
}

func cleanupDueRetiredRotatingIPs(ctx context.Context, cli *client.Client) {
	if getEnv("CF_API_TOKEN") == "" {
		return
	}
	if !ipv6RotateMu.TryLock() {
		return
	}
	defer ipv6RotateMu.Unlock()

	// 锁内：读取 state 并划分待清理列表，立即落盘（先移除待清理项）。
	// 这样网络 IO 在锁外执行，不会长时间阻塞其它 state 读写（saveCurrentIPs 等）。
	rotatingIPStateMu.Lock()
	rotatingPrefix, err := rotatingIPv6Prefix(getEnv("IPV6_SUBNET"))
	if err != nil {
		rotatingIPStateMu.Unlock()
		log.Printf("[ip-cleanup] subnet: %v", err)
		return
	}
	state := loadRotatingIPStateLocked()
	now := time.Now()

	var keep []retiredIPv6Entry
	var toCleanup []retiredIPv6Entry
	for _, entry := range state.Retired {
		entry.Service = strings.TrimSpace(entry.Service)
		entry.IP = normalizeIPv6(entry.IP)
		if entry.Service == "" || entry.IP == "" {
			continue
		}
		if entry.CleanupAfter.IsZero() || entry.CleanupAfter.After(now) {
			keep = append(keep, entry)
			continue
		}
		if state.Current[entry.Service] == entry.IP || !isRotatingIPv6InPrefix(entry.IP, rotatingPrefix) {
			continue
		}
		toCleanup = append(toCleanup, entry)
	}
	// 先把待清理项从持久化 state 中移除，失败时再在锁外回补。
	state.Retired = keep
	saveRotatingIPStateLocked(state)
	rotatingIPStateMu.Unlock()

	// 锁外：执行可能缓慢的网络 IO（findContainer + delContainerIP）。
	cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var failed []retiredIPv6Entry
	for _, entry := range toCleanup {
		id, err := findContainer(cleanupCtx, cli, entry.Service)
		if err != nil {
			log.Printf("[ip-cleanup] %s container not found for retired IP %s: %v", entry.Service, entry.IP, err)
			entry.CleanupAfter = now.Add(time.Minute)
			failed = append(failed, entry)
			continue
		}
		if err := delContainerIP(cleanupCtx, cli, id, entry.IP); err != nil {
			log.Printf("[ip-cleanup] failed to remove retired %s IPv6 %s: %v", entry.Service, entry.IP, err)
			entry.CleanupAfter = now.Add(time.Minute)
			failed = append(failed, entry)
			continue
		}
		log.Printf("[ip-cleanup] removed retired %s IPv6 %s", entry.Service, entry.IP)
	}

	// 短锁回写失败的项（延后重试）。
	if len(failed) > 0 {
		rotatingIPStateMu.Lock()
		state = loadRotatingIPStateLocked()
		state.Retired = append(state.Retired, failed...)
		saveRotatingIPStateLocked(state)
		rotatingIPStateMu.Unlock()
	}
}

func rotatingIPv6Prefix(subnet string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("parse subnet: %w", err)
	}
	prefix = prefix.Masked()
	addr := prefix.Addr()
	if !addr.Is6() || addr.Is4In6() {
		return netip.Prefix{}, fmt.Errorf("not an IPv6 subnet")
	}

	bytes := addr.As16()
	bytes[8] = 0
	bytes[9] = 0x0f
	for i := 10; i < len(bytes); i++ {
		bytes[i] = 0
	}
	return netip.PrefixFrom(netip.AddrFrom16(bytes), 80), nil
}

func normalizeIPv6(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is6() || addr.Is4In6() {
		return ip
	}
	return addr.String()
}

func isIPv6InPrefix(ip string, prefix netip.Prefix) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	return err == nil && addr.Is6() && !addr.Is4In6() && prefix.Contains(addr)
}

func isRotatingIPv6InPrefix(ip string, rotatingPrefix netip.Prefix) bool {
	return isIPv6InPrefix(ip, rotatingPrefix)
}

func selectStartupRotatingIP(basePrefix string, rotatingPrefix netip.Prefix, staticIP, dnsIP string, assigned, disallow, generateExclude []string) (startupIPv6Selection, error) {
	staticIP = normalizeIPv6(staticIP)
	dnsIP = normalizeIPv6(dnsIP)
	disallowed := ipv6Set(append(disallow, staticIP))

	if dnsIP != "" && dnsIP != staticIP && !disallowed[dnsIP] && isRotatingIPv6InPrefix(dnsIP, rotatingPrefix) {
		return startupIPv6Selection{IP: dnsIP}, nil
	}

	for _, ip := range assigned {
		ip = normalizeIPv6(ip)
		if ip == "" || ip == staticIP || disallowed[ip] || !isRotatingIPv6InPrefix(ip, rotatingPrefix) {
			continue
		}
		return startupIPv6Selection{IP: ip}, nil
	}

	ip, err := genRandomIPv6(basePrefix, generateExclude...)
	if err != nil {
		return startupIPv6Selection{}, err
	}
	return startupIPv6Selection{IP: ip, Generated: true}, nil
}

func ipv6Set(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = normalizeIPv6(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
