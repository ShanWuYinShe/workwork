package proxy

import (
	"admin/internal/platform"
	"admin/internal/runtime"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/client"
)

// IPv6State 是轮换地址状态的唯一 owner：路径来自 Layout，锁随实例走。
// 不再有包级可变路径/mutex；生产由 main 构造，测试用临时 Layout。
type IPv6State struct {
	path string
	mu   sync.Mutex
}

func NewIPv6State(layout runtime.Layout) *IPv6State {
	return &IPv6State{path: layout.IPv6StatePath()}
}

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

func (s *IPv6State) loadLocked() rotatingIPState {
	defaultState := rotatingIPState{Current: map[string]string{}}
	data, err := os.ReadFile(s.path)
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
		// 与 platform.LoadJSONFile 同策略:损坏现场隔离为 .corrupt——下一次
		// 保存会静默覆盖原文件,不隔离则 Retired 清理队列无法事后恢复。
		log.Printf("ERROR: parse rotating IP state: %v", err)
		quarantine := s.path + ".corrupt"
		if qerr := os.Rename(s.path, quarantine); qerr != nil {
			log.Printf("ERROR: quarantine rotating IP state %s: %v", quarantine, qerr)
		}
		platform.NotifyWebhook("data.corrupt", "状态文件损坏已隔离: "+s.path, map[string]string{
			"path": s.path, "quarantined_as": quarantine,
		})
		return defaultState
	}
	state.Current = legacy
	normalizeRotatingIPState(&state)
	return state
}

func (s *IPv6State) saveLocked(state rotatingIPState) {
	normalizeRotatingIPState(&state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("ERROR: marshal rotating IP state: %v", err)
		return
	}
	if err := platform.AtomicWriteFile(s.path, data, 0600); err != nil {
		log.Printf("ERROR: save rotating IP state: %v", err)
	}
}

func normalizeRotatingIPState(state *rotatingIPState) {
	if state.Current == nil {
		state.Current = map[string]string{}
	}
	for service, ip := range state.Current {
		if normalized := platform.NormalizeIPv6(ip); normalized != "" {
			state.Current[service] = normalized
		}
	}

	dedup := make(map[string]retiredIPv6Entry)
	for _, entry := range state.Retired {
		entry.Service = strings.TrimSpace(entry.Service)
		entry.IP = platform.NormalizeIPv6(entry.IP)
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
	sort.Slice(state.Retired, func(i, j int) bool {
		if state.Retired[i].Service != state.Retired[j].Service {
			return state.Retired[i].Service < state.Retired[j].Service
		}
		return state.Retired[i].IP < state.Retired[j].IP
	})
}

// SaveCurrent persists the active rotating IPs and the retired-IP cleanup queue.
func (s *IPv6State) SaveCurrent(xrayIP, nginxIP string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.loadLocked()
	if xrayIP != "" {
		state.Current["xray"] = platform.NormalizeIPv6(xrayIP)
	}
	if nginxIP != "" {
		state.Current["nginx"] = platform.NormalizeIPv6(nginxIP)
	}
	s.saveLocked(state)
}

func (s *IPv6State) Retire(entries ...retiredIPv6Entry) {
	if len(entries) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.loadLocked()
	now := time.Now()
	for _, entry := range entries {
		entry.Service = strings.TrimSpace(entry.Service)
		entry.IP = platform.NormalizeIPv6(entry.IP)
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
	s.saveLocked(state)
}

func (s *IPv6State) RetireStaleAssigned(service string, assigned []string, keepIP string, rotatingPrefix netip.Prefix) {
	keepIP = platform.NormalizeIPv6(keepIP)
	var entries []retiredIPv6Entry
	for _, ip := range assigned {
		ip = platform.NormalizeIPv6(ip)
		if ip == "" || ip == keepIP || !isRotatingIPv6InPrefix(ip, rotatingPrefix) {
			continue
		}
		entries = append(entries, retiredIPv6Entry{Service: service, IP: ip})
	}
	s.Retire(entries...)
}

func (s *IPv6State) CleanupDue(ctx context.Context, cli *client.Client) {
	if platform.GetEnv("CF_API_TOKEN") == "" {
		return
	}
	if !ipv6RotateMu.TryLock() {
		return
	}
	defer ipv6RotateMu.Unlock()

	// 锁内：读取 state 并划分待清理列表，立即落盘（先移除待清理项）。
	// 这样网络 IO 在锁外执行，不会长时间阻塞其它 state 读写（SaveCurrent 等）。
	s.mu.Lock()
	rotatingPrefix, err := rotatingIPv6Prefix(platform.GetEnv("IPV6_SUBNET"))
	if err != nil {
		s.mu.Unlock()
		log.Printf("[ip-cleanup] subnet: %v", err)
		return
	}
	state := s.loadLocked()
	now := time.Now()

	var keep []retiredIPv6Entry
	var toCleanup []retiredIPv6Entry
	for _, entry := range state.Retired {
		entry.Service = strings.TrimSpace(entry.Service)
		entry.IP = platform.NormalizeIPv6(entry.IP)
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
	s.saveLocked(state)
	s.mu.Unlock()

	// 锁外：执行可能缓慢的网络 IO（platform.FindContainer + platform.DelContainerIP）。
	cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var failed []retiredIPv6Entry
	for _, entry := range toCleanup {
		id, err := platform.FindContainer(cleanupCtx, cli, entry.Service)
		if err != nil {
			log.Printf("[ip-cleanup] %s container not found for retired IP %s: %v", entry.Service, entry.IP, err)
			entry.CleanupAfter = now.Add(time.Minute)
			failed = append(failed, entry)
			continue
		}
		if err := platform.DelContainerIP(cleanupCtx, cli, id, entry.IP); err != nil {
			log.Printf("[ip-cleanup] failed to remove retired %s IPv6 %s: %v", entry.Service, entry.IP, err)
			entry.CleanupAfter = now.Add(time.Minute)
			failed = append(failed, entry)
			continue
		}
		log.Printf("[ip-cleanup] removed retired %s IPv6 %s", entry.Service, entry.IP)
	}

	// 短锁回写失败的项（延后重试）。
	if len(failed) > 0 {
		s.mu.Lock()
		state = s.loadLocked()
		state.Retired = append(state.Retired, failed...)
		s.saveLocked(state)
		s.mu.Unlock()
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

func isIPv6InPrefix(ip string, prefix netip.Prefix) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	return err == nil && addr.Is6() && !addr.Is4In6() && prefix.Contains(addr)
}

func isRotatingIPv6InPrefix(ip string, rotatingPrefix netip.Prefix) bool {
	return isIPv6InPrefix(ip, rotatingPrefix)
}

func selectStartupRotatingIP(basePrefix string, rotatingPrefix netip.Prefix, staticIP, dnsIP string, assigned, disallow, generateExclude []string) (startupIPv6Selection, error) {
	staticIP = platform.NormalizeIPv6(staticIP)
	dnsIP = platform.NormalizeIPv6(dnsIP)
	disallowed := ipv6Set(append(disallow, staticIP))

	if dnsIP != "" && dnsIP != staticIP && !disallowed[dnsIP] && isRotatingIPv6InPrefix(dnsIP, rotatingPrefix) {
		return startupIPv6Selection{IP: dnsIP}, nil
	}

	for _, ip := range assigned {
		ip = platform.NormalizeIPv6(ip)
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
		value = platform.NormalizeIPv6(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}
