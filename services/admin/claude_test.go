package main

import (
	"reflect"
	"testing"
)

func TestKeepNewestDownloads(t *testing.T) {
	mk := func(versions ...string) []claudeStoredDownload {
		out := make([]claudeStoredDownload, 0, len(versions))
		for _, v := range versions {
			out = append(out, claudeStoredDownload{Version: v, Filename: "claude-" + v + "-linux-x64.tar.gz"})
		}
		return out
	}

	t.Run("保留最近 maxKeep 条", func(t *testing.T) {
		got := keepNewestDownloads(mk("1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0"), 3)
		want := mk("1.2.0", "1.3.0", "1.4.0")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("keepNewestDownloads() = %+v, want %+v", got, want)
		}
	})

	t.Run("不超限时原样保留", func(t *testing.T) {
		entries := mk("1.0.0", "1.1.0")
		got := keepNewestDownloads(entries, 5)
		if !reflect.DeepEqual(got, entries) {
			t.Fatalf("keepNewestDownloads() = %+v, want %+v", got, entries)
		}
	})

	t.Run("空列表不 panic", func(t *testing.T) {
		got := keepNewestDownloads(nil, 3)
		if len(got) != 0 {
			t.Fatalf("keepNewestDownloads(nil) = %+v, want empty", got)
		}
	})

	t.Run("返回副本不共享底层数组", func(t *testing.T) {
		entries := mk("1.0.0", "1.1.0", "1.2.0", "1.3.0")
		got := keepNewestDownloads(entries, 2)
		got[0].Version = "mutated"
		if entries[2].Version == "mutated" {
			t.Fatal("keepNewestDownloads() 返回结果与入参共享底层数组")
		}
	})
}
