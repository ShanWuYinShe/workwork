package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

var (
	cfZoneCache   string
	cfZoneCacheMu sync.Mutex
)

func cfToken() string { return getEnv("CF_API_TOKEN") }

func cfResolveZoneID(domain string) (string, error) {
	cfZoneCacheMu.Lock()
	defer cfZoneCacheMu.Unlock()
	if cfZoneCache != "" {
		return cfZoneCache, nil
	}
	// Extract root domain: try parent domains progressively
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		cr, err := cfRequest("GET", fmt.Sprintf("/zones?name=%s", candidate), nil)
		if err != nil {
			continue
		}
		var zones []cfZone
		if err := json.Unmarshal(cr.Result, &zones); err != nil {
			continue
		}
		if len(zones) > 0 {
			cfZoneCache = zones[0].ID
			return cfZoneCache, nil
		}
	}
	return "", fmt.Errorf("zone not found for domain %s", domain)
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
	cfClient := &http.Client{Timeout: 30 * time.Second}
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
	cr, err := cfRequest("GET", fmt.Sprintf("/zones/%s/dns_records?type=AAAA&name=%s", zoneID, domain), nil)
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
		json.Unmarshal(cr.Result, &updated)
		return &updated, nil
	}
	cr, err := cfRequest("POST", fmt.Sprintf("/zones/%s/dns_records", zoneID), record)
	if err != nil {
		return nil, err
	}
	var created cfDNSRecord
	json.Unmarshal(cr.Result, &created)
	return &created, nil
}
