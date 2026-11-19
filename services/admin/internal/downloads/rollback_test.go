package downloads

// 回滚路径与并发登记回归测试。此前 downloadArchive 的失败回滚不分辨产物
// 归属：manifest 读失败时无条件删除刚 rename 的文件，会把并发（或历史）
// 已登记的同名产物一并删掉，留下「有条目无文件」的 404 条目；本组测试
// 锁定三件事——读失败保留产物、写失败回滚删除产物、并发同名下载只有一条
// manifest 条目且产物在位。

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// manifest 存在但损坏（LoadJSON 隔离为 .corrupt 并返回错误）时，产物必须
// 保留：旧行为无条件删除，同名条目（历史登记 / 并发登记）会变成 404。
func TestDownloadArchiveKeepsFileWhenManifestUnreadable(t *testing.T) {
	payload := []byte("fake-xray-zip-bytes")
	s := newDigestTestService(t)
	withStubDownloadClient(t, payload)
	if err := os.WriteFile(s.manifestPath, []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.downloadArchive(context.Background(), "Xray-1.2.3-linux-64.zip", "v1.2.3", "linux-64", testAssetURL, "", nil)
	if err == nil {
		t.Fatal("manifest 读失败应返回错误")
	}
	if _, statErr := os.Stat(filepath.Join(s.dir, "Xray-1.2.3-linux-64.zip")); statErr != nil {
		t.Fatalf("manifest 读失败后产物必须保留（删除会产生 404 条目）: %v", statErr)
	}
}

// manifest 读成功但写失败（目录只读）时，回滚必须删除产物：条目未落盘，
// 留文件就是列表不可见、无人清理的孤儿。
// manifest 放进只读子目录，下载目录保持可写（下载临时文件要在其中创建）。
func TestDownloadArchiveRollbackRemovesFileWhenManifestSaveFails(t *testing.T) {
	payload := []byte("fake-xray-zip-bytes")
	dir := t.TempDir()
	meta := filepath.Join(dir, "meta")
	if err := os.MkdirAll(meta, 0755); err != nil {
		t.Fatal(err)
	}
	s := &downloadService{
		dir:          dir,
		manifestPath: filepath.Join(meta, "downloads.json"),
		platforms:    []downloadPlatform{{Label: "Linux x64", Plat: "linux-64"}},
	}
	withStubDownloadClient(t, payload)
	if err := os.Chmod(meta, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(meta, 0755) })

	_, _, err := s.downloadArchive(context.Background(), "Xray-1.2.3-linux-64.zip", "v1.2.3", "linux-64", testAssetURL, "", nil)
	if err == nil {
		t.Fatal("manifest 写失败应返回错误")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "Xray-1.2.3-linux-64.zip")); !os.IsNotExist(statErr) {
		t.Fatalf("manifest 写失败后回滚必须删除产物（防孤儿）: %v", statErr)
	}
}

// 并发下载同一 version+plat：全部成功、只有一条 manifest 条目、产物在位。
// fileMu 串行化 rename→登记段后，-race 下不得出现死锁或数据竞争。
func TestDownloadArchiveConcurrentSameFilename(t *testing.T) {
	payload := []byte("fake-xray-zip-bytes")
	s := newDigestTestService(t)
	withStubDownloadClient(t, payload)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = s.downloadArchive(context.Background(), "Xray-1.2.3-linux-64.zip", "v1.2.3", "linux-64", testAssetURL, "", nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发下载 %d 失败: %v", i, err)
		}
	}
	stored, err := s.listStored()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("并发同名下载应只有一条 manifest 条目, got %d: %+v", len(stored), stored)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "Xray-1.2.3-linux-64.zip")); err != nil {
		t.Fatalf("产物必须在位: %v", err)
	}
}
