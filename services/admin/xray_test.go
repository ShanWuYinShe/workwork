package main

import (
	"reflect"
	"testing"
)

func TestKeepNewestXrayDownloads(t *testing.T) {
	mk := func(versions ...string) []xrayStoredDownload {
		out := make([]xrayStoredDownload, 0, len(versions))
		for _, v := range versions {
			out = append(out, xrayStoredDownload{Version: v, Filename: "Xray-" + v + "-linux-64.zip"})
		}
		return out
	}

	t.Run("保留最近 maxKeep 条", func(t *testing.T) {
		got := keepNewestXrayDownloads(mk("1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0"), 2)
		want := mk("1.3.0", "1.4.0")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("keepNewestXrayDownloads() = %+v, want %+v", got, want)
		}
	})

	t.Run("不超限时原样保留", func(t *testing.T) {
		entries := mk("1.0.0", "1.1.0")
		got := keepNewestXrayDownloads(entries, 5)
		if !reflect.DeepEqual(got, entries) {
			t.Fatalf("keepNewestXrayDownloads() = %+v, want %+v", got, entries)
		}
	})

	t.Run("空列表不 panic", func(t *testing.T) {
		got := keepNewestXrayDownloads(nil, 3)
		if len(got) != 0 {
			t.Fatalf("keepNewestXrayDownloads(nil) = %+v, want empty", got)
		}
	})
}
