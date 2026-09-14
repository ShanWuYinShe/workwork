package media

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// mkRecord 构造最小可用的发布记录。
func mkRecord(code int, name string) mediaAppReleaseManifest {
	return mediaAppReleaseManifest{
		VersionCode: code,
		VersionName: name,
		APKFilename: "media-center-app-v" + name + ".apk",
	}
}

func TestUpsertReleaseRecordInsertsAndSortsDesc(t *testing.T) {
	records := upsertReleaseRecord(nil, mkRecord(2, "1.1.0"))
	records = upsertReleaseRecord(records, mkRecord(5, "1.2.0"))
	records = upsertReleaseRecord(records, mkRecord(3, "1.1.1"))

	var codes []int
	for _, rec := range records {
		codes = append(codes, rec.VersionCode)
	}
	want := []int{5, 3, 2}
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("codes = %v, want %v", codes, want)
	}
}

func TestUpsertReleaseRecordReplacesSameVersionCode(t *testing.T) {
	records := upsertReleaseRecord(nil, mkRecord(2, "1.1.0"))
	replacement := mkRecord(2, "1.1.0")
	replacement.SHA256 = "abcdef0123456789"
	records = upsertReleaseRecord(records, replacement)

	if len(records) != 1 {
		t.Fatalf("len = %d, want 1 (same versionCode must replace, not append)", len(records))
	}
	if records[0].SHA256 != replacement.SHA256 {
		t.Fatalf("sha256 = %q, want replacement %q", records[0].SHA256, replacement.SHA256)
	}
}

func TestMediaCenterAppUpdateHistoryPath(t *testing.T) {
	tests := []struct {
		variant string
		want    string
	}{
		{variant: "", want: "/app/data/generated/media_center/app-history.json"},
		{variant: "normal", want: "/app/data/generated/media_center/app-history.json"},
		{variant: "legacy", want: "/app/data/generated/media_center/app-history-legacy.json"},
		{variant: "../evil", want: "/app/data/generated/media_center/app-history.json"},
	}
	for _, tt := range tests {
		if got := appUpdateHistoryPath(tt.variant); got != tt.want {
			t.Errorf("appUpdateHistoryPath(%q) = %q, want %q", tt.variant, got, tt.want)
		}
	}
}

func TestReleaseHistoryEntryJSONShape(t *testing.T) {
	// 内嵌 manifest 的字段必须平铺进 JSON（客户端直接读 versionCode 等顶层字段），
	// 同时携带 current / apkExists 标注。
	entry := releaseHistoryEntry{mediaAppReleaseManifest: mkRecord(7, "1.3.0"), Current: true, ApkExists: true}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"versionCode", "versionName", "apkFilename", "current", "apkExists"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("missing key %q in %s", key, data)
		}
	}
}

func TestPruneOrphanAppAPKsInDir(t *testing.T) {
	// 集成验证 prune 的保留规则：manifest 引用的、历史引用的、30 秒内新鲜的 APK
	// 必须存活；无引用的旧 APK 必须被删除（这是误删即事故的高危路径）。
	dir := t.TempDir()
	mkFile := func(name string, data []byte, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if age > 0 {
			stale := time.Now().Add(-age)
			if err := os.Chtimes(p, stale, stale); err != nil {
				t.Fatalf("chtimes %s: %v", name, err)
			}
		}
	}

	apk := map[string]bool{}                          // name -> 是否应存活
	apk["media-center-app-v1.1.0.apk"] = true         // 当前 manifest 引用
	apk["media-center-app-legacy-v0.9.0.apk"] = true  // 历史记录引用
	apk["media-center-app-v9.9.8-fresh.apk"] = true   // 无引用但新鲜(rename→写盘窗口兜底)
	apk["media-center-app-v9.9.9-orphan.apk"] = false // 无引用且陈旧 → 应被清理
	for name := range apk {
		age := time.Minute
		if apk[name] && name == "media-center-app-v9.9.8-fresh.apk" {
			age = 0
		}
		mkFile(name, []byte("apk-bytes"), age)
	}
	mkFile("app-update.json", []byte(`{"versionCode":1,"versionName":"1.1.0","apkFilename":"media-center-app-v1.1.0.apk"}`), 0)
	mkFile("app-history-legacy.json", []byte(`[{"versionCode":1,"versionName":"0.9.0","apkFilename":"media-center-app-legacy-v0.9.0.apk"}]`), 0)

	pruneOrphanAppAPKsInDir(dir, nil)

	for name, wantKept := range apk {
		_, err := os.Stat(filepath.Join(dir, name))
		if wantKept && err != nil {
			t.Errorf("%s should be kept, stat: %v", name, err)
		}
		if !wantKept && !os.IsNotExist(err) {
			t.Errorf("%s should be pruned (err=%v)", name, err)
		}
	}
}
