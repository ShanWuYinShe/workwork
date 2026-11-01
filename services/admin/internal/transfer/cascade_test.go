package transfer

import (
	"admin/internal/runtime"
	"os"
	"testing"
)

// TestDeleteUploadsByUploader 验证删用户级联:只删目标用户的文件与条目,
// TotalSize 同步扣减,其他用户的文件不受影响;无上传时返回 0。
func TestDeleteUploadsByUploader(t *testing.T) {
	store := NewStore(runtime.NewLayout(t.TempDir(), t.TempDir()))
	if err := os.MkdirAll(store.dir(), 0755); err != nil {
		t.Fatal(err)
	}
	uploaderA, uploaderB := "alice", "bob"
	write := func(code, filename, uploader string, size int64) {
		p := store.filePath(code, filename)
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := store.loadLocked()
		if err != nil {
			t.Fatal(err)
		}
		m.Files = append(m.Files, transferFile{
			Code: code, Filename: filename, Size: size, Uploader: uploader,
		})
		m.TotalSize += size
		if err := store.saveLocked(m); err != nil {
			t.Fatal(err)
		}
	}
	write("codeaaa", "a1.bin", uploaderA, 100)
	write("codebbb", "a2.bin", uploaderA, 200)
	write("codeccc", "b1.bin", uploaderB, 300)

	removed, err := store.DeleteUploadsByUploader(uploaderA)
	if err != nil || removed != 2 {
		t.Fatalf("removed=%d err=%v, want 2/nil", removed, err)
	}
	// A 的磁盘文件已删,只剩 B 的(manifest.json 是状态文件,不算数据文件)。
	entries, err := os.ReadDir(store.dir())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.Name() != "manifest.json" {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 || names[0] != "codeccc.bin" {
		t.Fatalf("only bob's file should remain on disk, got %v", names)
	}
	m, err := store.loadLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 1 || m.Files[0].Uploader != uploaderB {
		t.Fatalf("manifest should keep only bob's entry, got %+v", m.Files)
	}
	if m.TotalSize != 300 {
		t.Fatalf("TotalSize=%d, want 300", m.TotalSize)
	}

	// 再删一次:无条目,返回 0。
	if n, err := store.DeleteUploadsByUploader(uploaderA); err != nil || n != 0 {
		t.Fatalf("second delete removed=%d err=%v, want 0/nil", n, err)
	}
}
