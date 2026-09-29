package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCleanupStaleTempFiles 验证各写入路径的临时文件残留被清理,
// 正常数据文件不受影响。
func TestCleanupStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "generated", "nginx")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{
		// AtomicWriteFile 模式:.<base>.tmp-<rand>
		"users.json":                 false, // 正常数据
		".users.json.tmp-123456":     true,
		".rotating-ips.json.tmp-abc": true,
		// 不应被误删的:点开头但非临时模式
		".htpasswd":            false,
		".internal-token":      false,
		"generated-nginx.conf": false,
	}
	for name := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 子目录扫描:generated/ 下的同模式残留同样被清理。
	subFile := filepath.Join("generated", "nginx", ".blocked-ips.conf.tmp-1")
	if err := os.WriteFile(filepath.Join(dir, subFile), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	files[subFile] = true

	removed := CleanupStaleTempFiles(dir)
	if removed == 0 {
		t.Fatal("expected stale temp files to be removed")
	}
	for name, wantRemoved := range files {
		path := filepath.Join(dir, name)
		_, err := os.Stat(path)
		exists := err == nil
		if wantRemoved && exists {
			t.Errorf("%s should have been removed", name)
		}
		if !wantRemoved && !exists {
			t.Errorf("%s should have been kept", name)
		}
	}
}
