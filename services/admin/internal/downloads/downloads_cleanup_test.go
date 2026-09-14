package downloads

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPurgeStaleDownloadTemps 验证启动清理只删除残留的 ".download-*" 临时
// 文件，保留正常下载产物与目录。回归保护：download{Claude,Xray}Archive 的
// 临时文件命名约定（CreateTemp 的 filename+".download-*"）与清理逻辑必须
// 保持一致——改名会导致残留文件既不可下载也不可删除。
func TestPurgeStaleDownloadTemps(t *testing.T) {
	dir := t.TempDir()
	origC, origX := claudeDownloads.dir, xrayDownloads.dir
	claudeDownloads.dir, xrayDownloads.dir = dir, dir
	defer func() { claudeDownloads.dir, xrayDownloads.dir = origC, origX }()

	keep := []string{"claude-1.0.0-linux-x64.tar.gz", "Xray-1.2.3-linux-64.zip"}
	drop := []string{
		"claude-1.0.0-linux-x64.tar.gz.download-123456",
		"Xray-9.9.9-linux-64.zip.download-987",
	}
	for _, n := range append(keep, drop...) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// 名称含 ".download-" 的子目录不应被删除（本函数只清文件）
	sub := filepath.Join(dir, "sub.download-dir")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	PurgeStaleDownloadTemps()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	for _, n := range keep {
		if !got[n] {
			t.Errorf("正常文件被误删: %s", n)
		}
	}
	for _, n := range drop {
		if got[n] {
			t.Errorf("残留临时文件未被清理: %s", n)
		}
	}
	if !got["sub.download-dir"] {
		t.Error("子目录被误删")
	}
	if len(entries) != len(keep)+1 {
		t.Errorf("剩余条目数 = %d, 期望 %d (正常文件 + 子目录)", len(entries), len(keep)+1)
	}
}
