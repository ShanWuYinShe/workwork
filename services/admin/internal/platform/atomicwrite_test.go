package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Concurrent writers to the same path must not corrupt the file or fail with
// rename ENOENT. A fixed-name temp file (path+".tmp") could not satisfy this;
// unique temp names per write can.
func TestAtomicWriteFileConcurrentSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.txt")

	const writers = 16
	valid := make(map[string]bool, writers)
	for i := 0; i < writers; i++ {
		valid[fmt.Sprintf("payload-%02d", i)] = true
	}

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("payload-%02d", n))
			if err := AtomicWriteFile(path, data, 0o644); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent AtomicWriteFile failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	if !valid[string(got)] {
		t.Fatalf("final content is not one of the written payloads: %q", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat final file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("expected perm 0644, got %o", info.Mode().Perm())
	}

	// No leftover temp files should remain in the directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "shared.txt" {
			t.Fatalf("unexpected leftover file in dir: %q", e.Name())
		}
	}
}
