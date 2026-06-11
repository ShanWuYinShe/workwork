package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotatingIPv6Prefix(t *testing.T) {
	prefix, err := rotatingIPv6Prefix("2605:a141:2322:2623::/64")
	if err != nil {
		t.Fatalf("rotatingIPv6Prefix: %v", err)
	}

	tests := []struct {
		ip   string
		want bool
	}{
		{"2605:a141:2322:2623:f::1", true},
		{"2605:a141:2322:2623:000f:abcd::1", true},
		{"2605:a141:2322:2623:1::12", false},
		{"2605:a141:2322:2624:f::1", false},
		{"not-an-ip", false},
	}

	for _, tt := range tests {
		if got := isRotatingIPv6InPrefix(tt.ip, prefix); got != tt.want {
			t.Fatalf("isRotatingIPv6InPrefix(%q) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestSelectStartupRotatingIPPrefersDNSThenAssigned(t *testing.T) {
	basePrefix, err := parseSubnetPrefix("2605:a141:2322:2623::/64")
	if err != nil {
		t.Fatalf("parseSubnetPrefix: %v", err)
	}
	rotatingPrefix, err := rotatingIPv6Prefix("2605:a141:2322:2623::/64")
	if err != nil {
		t.Fatalf("rotatingIPv6Prefix: %v", err)
	}

	selection, err := selectStartupRotatingIP(
		basePrefix,
		rotatingPrefix,
		"2605:a141:2322:2623:1::12",
		"2605:a141:2322:2623:000f::99",
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("selectStartupRotatingIP dns: %v", err)
	}
	if selection.IP != "2605:a141:2322:2623:f::99" || selection.Generated {
		t.Fatalf("unexpected DNS selection: %#v", selection)
	}

	selection, err = selectStartupRotatingIP(
		basePrefix,
		rotatingPrefix,
		"2605:a141:2322:2623:1::12",
		"2605:a141:2322:2623:1::12",
		[]string{"2605:a141:2322:2623:f::abcd"},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("selectStartupRotatingIP assigned: %v", err)
	}
	if selection.IP != "2605:a141:2322:2623:f::abcd" || selection.Generated {
		t.Fatalf("unexpected assigned selection: %#v", selection)
	}
}

func TestRotatingIPStateSupportsLegacyAndRetiredQueue(t *testing.T) {
	oldPath := rotatingIPStatePath
	rotatingIPStatePath = filepath.Join(t.TempDir(), "rotating-ips.json")
	t.Cleanup(func() { rotatingIPStatePath = oldPath })

	if err := os.WriteFile(rotatingIPStatePath, []byte(`{"xray":"2605:a141:2322:2623:000f::1","nginx":"2605:a141:2322:2623:000f::2"}`), 0600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	rotatingIPStateMu.Lock()
	state := loadRotatingIPStateLocked()
	rotatingIPStateMu.Unlock()
	if state.Current["xray"] != "2605:a141:2322:2623:f::1" || state.Current["nginx"] != "2605:a141:2322:2623:f::2" {
		t.Fatalf("unexpected legacy state: %#v", state.Current)
	}

	saveCurrentIPs("2605:a141:2322:2623:f::10", "2605:a141:2322:2623:f::20")
	before := time.Now().Add(rotatingIPv6CleanupGrace - time.Second)
	retireRotatingIPs(
		retiredIPv6Entry{Service: "xray", IP: "2605:a141:2322:2623:f::1"},
		retiredIPv6Entry{Service: "xray", IP: "2605:a141:2322:2623:f::10"},
	)

	rotatingIPStateMu.Lock()
	state = loadRotatingIPStateLocked()
	rotatingIPStateMu.Unlock()

	if state.Current["xray"] != "2605:a141:2322:2623:f::10" {
		t.Fatalf("unexpected current xray IP: %#v", state.Current)
	}
	if len(state.Retired) != 1 {
		t.Fatalf("expected only non-current retired IP, got %#v", state.Retired)
	}
	if state.Retired[0].IP != "2605:a141:2322:2623:f::1" || state.Retired[0].CleanupAfter.Before(before) {
		t.Fatalf("unexpected retired entry: %#v", state.Retired[0])
	}
}
