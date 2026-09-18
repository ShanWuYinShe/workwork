package platform

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestLoadJSONFileQuarantinesCorruptFile 锁定隔离行为:manifest 类状态文件
// 截断/损坏时,若不隔离,下一次写入会把损坏字节静默覆盖、现场丢失。
func TestLoadJSONFileQuarantinesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	corrupt := []byte(`{"files":[{"code":"abc"}`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	var dest struct {
		Files []string `json:"files"`
	}
	LoadJSONFile(path, &dest)

	if len(dest.Files) != 0 {
		t.Fatalf("dest should stay zero-valued on corruption, got %+v", dest)
	}
	// 原文件已改名为 .corrupt,保留原始字节供事后恢复。
	q := path + ".corrupt"
	got, err := os.ReadFile(q)
	if err != nil {
		t.Fatalf("corrupt file not quarantined: %v", err)
	}
	if string(got) != string(corrupt) {
		t.Fatalf("quarantined bytes differ: %q", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original path should be gone after quarantine, stat err=%v", err)
	}
}

// TestLoadJSONFileMissingKeepsZero 文件不存在时保持零值且不产生隔离文件。
func TestLoadJSONFileMissingKeepsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent.json")
	var dest struct {
		Files []string `json:"files"`
	}
	LoadJSONFile(path, &dest)
	if len(dest.Files) != 0 {
		t.Fatalf("dest should stay zero for missing file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing file should not create artifacts: %v %v", entries, err)
	}
}

// TestAcquireUploadSlot 验证并发闸门:额度满时阻塞,释放后可再取;
// ctx 取消时放弃(客户端断开不占用排队位)。
func TestAcquireUploadSlot(t *testing.T) {
	releases := make([]func(), 0, uploadSlots)
	for i := 0; i < uploadSlots; i++ {
		rel, ok := AcquireUploadSlot(context.Background())
		if !ok {
			t.Fatalf("acquire %d/%d should succeed", i+1, uploadSlots)
		}
		releases = append(releases, rel)
	}
	// releases[0] 在下方转让给排队 goroutine,其 token 由 goroutine 的 rel
	// 归还;cleanup 只负责归还尚未动过的 releases[1](token 收发必须配平,
	// 多余的 receive 会在空 channel 上永久阻塞)。
	t.Cleanup(releases[1])

	// 额度占满后:ctx 已取消的请求立即失败而非永久阻塞。
	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel()
	if rel, ok := AcquireUploadSlot(ctxCancel); ok {
		rel()
		t.Fatal("acquire with cancelled ctx should fail while gate is full")
	}

	// 排队中的请求在释放后获得额度(异步验证,超时 1s)。
	var wg sync.WaitGroup
	got := make(chan func(), 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		rel, ok := AcquireUploadSlot(context.Background())
		if ok {
			got <- rel
		}
	}()
	releases[0]()
	wg.Wait()
	select {
	case rel := <-got:
		rel()
	default:
		t.Fatal("queued acquire did not proceed after release")
	}
}
