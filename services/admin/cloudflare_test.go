package main

import (
	"reflect"
	"testing"
)

func TestCFZoneCandidatesIncludesApex(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		want   []string
	}{
		{
			name:   "apex",
			domain: "yinghuoren.com",
			want:   []string{"yinghuoren.com"},
		},
		{
			name:   "subdomain",
			domain: "xray.yinghuoren.com",
			want:   []string{"xray.yinghuoren.com", "yinghuoren.com"},
		},
		{
			name:   "trailing dot",
			domain: "xray.yinghuoren.com.",
			want:   []string{"xray.yinghuoren.com", "yinghuoren.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfZoneCandidates(tt.domain); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("cfZoneCandidates(%q) = %#v, want %#v", tt.domain, got, tt.want)
			}
		})
	}
}
