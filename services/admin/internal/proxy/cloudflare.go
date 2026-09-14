package proxy

import (
	"admin/internal/platform"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const cfAPIBase = "https://api.cloudflare.com/client/v4"

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

type cfResponse struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfZoneCacheEntry struct {
	ID        string
	CheckedAt time.Time
}

var (
	cfZoneCache   = make(map[string]cfZoneCacheEntry)
	cfZoneCacheMu sync.Mutex
)

var cfClient *http.Client

func init() {
	cfClient = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		Timeout: 30 * time.Second,
	}
}

func cfToken() string { return platform.GetEnv("CF_API_TOKEN") }

func cfResolveZoneID(domain string) (string, error) {
	// 先查缓存(锁内只做读):未命中时释放锁再发网络请求,避免 CF API 往返
	// (最长 30s 超时)期间所有 CF 操作串行化;结果回写缓存时允许少量重复查询。
	cfZoneCacheMu.Lock()
	for _, candidate := range cfZoneCandidates(domain) {
		if entry, ok := cfZoneCache[candidate]; ok && time.Since(entry.CheckedAt) < time.Hour {
			cfZoneCacheMu.Unlock()
			return entry.ID, nil
		}
	}
	cfZoneCacheMu.Unlock()

	// 锁外执行 CF API 查询
	for _, candidate := range cfZoneCandidates(domain) {
		cr, err := cfRequest("GET", fmt.Sprintf("/zones?name=%s", url.QueryEscape(candidate)), nil)
		if err != nil {
			continue
		}
		var zones []cfZone
		if err := json.Unmarshal(cr.Result, &zones); err != nil {
			continue
		}
		if len(zones) > 0 {
			cfZoneCacheMu.Lock()
			// 缓存带 TTL，Cloudflare zone 被迁移/重命名后能自愈刷新
			cfZoneCache[candidate] = cfZoneCacheEntry{ID: zones[0].ID, CheckedAt: time.Now()}
			cfZoneCacheMu.Unlock()
			return zones[0].ID, nil
		}
	}
	return "", fmt.Errorf("zone not found for domain %s", domain)
}

func cfZoneCandidates(domain string) []string {
	domain = strings.Trim(strings.TrimSpace(domain), ".")
	parts := strings.Split(domain, ".")
	var candidates []string
	for i := 0; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		if candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func cfRequest(method, path string, body any) (*cfResponse, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, cfAPIBase+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfToken())
	req.Header.Set("Content-Type", "application/json")
	resp, err := cfClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cf request failed: %w", err)
	}
	defer resp.Body.Close()
	var cr cfResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("cf decode failed: %w", err)
	}
	if !cr.Success && len(cr.Errors) > 0 {
		return nil, fmt.Errorf("cf api error: %s (code %d)", cr.Errors[0].Message, cr.Errors[0].Code)
	}
	return &cr, nil
}

func cfFindAAAARecord(domain string) (*cfDNSRecord, error) {
	zoneID, err := cfResolveZoneID(domain)
	if err != nil {
		return nil, err
	}
	cr, err := cfRequest("GET", fmt.Sprintf("/zones/%s/dns_records?type=AAAA&name=%s", zoneID, url.QueryEscape(domain)), nil)
	if err != nil {
		return nil, err
	}
	var records []cfDNSRecord
	if err := json.Unmarshal(cr.Result, &records); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return &records[0], nil
}

func cfUpsertAAAARecord(domain, ip string) (*cfDNSRecord, error) {
	zoneID, err := cfResolveZoneID(domain)
	if err != nil {
		return nil, err
	}
	existing, err := cfFindAAAARecord(domain)
	if err != nil {
		return nil, err
	}
	record := cfDNSRecord{Type: "AAAA", Name: domain, Content: ip, TTL: 60}
	if existing != nil {
		cr, err := cfRequest("PATCH", fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, existing.ID), record)
		if err != nil {
			return nil, err
		}
		var updated cfDNSRecord
		if err := json.Unmarshal(cr.Result, &updated); err != nil {
			return nil, fmt.Errorf("failed to parse PATCH response: %w", err)
		}
		return &updated, nil
	}
	cr, err := cfRequest("POST", fmt.Sprintf("/zones/%s/dns_records", zoneID), record)
	if err != nil {
		return nil, err
	}
	var created cfDNSRecord
	if err := json.Unmarshal(cr.Result, &created); err != nil {
		return nil, fmt.Errorf("failed to parse POST response: %w", err)
	}
	return &created, nil
}

func cfDeleteAAAARecord(domain string) error {
	zoneID, err := cfResolveZoneID(domain)
	if err != nil {
		return err
	}
	existing, err := cfFindAAAARecord(domain)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	_, err = cfRequest("DELETE", fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, existing.ID), nil)
	return err
}
