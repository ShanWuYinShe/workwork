package transfer

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) CleanupOrphanTempFiles() {
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("WARNING: read transfer dir for orphan cleanup: %v", err)
		}
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".upload-") || !strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir(), e.Name())); err != nil {
			log.Printf("WARNING: remove orphan upload temp %s: %v", e.Name(), err)
		}
	}
}

// cleanupExpiredTransferFiles 删除过期或达下载上限的文件，返回释放的字节数
func (s *Store) cleanupExpiredTransferFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.loadLocked()
	if err != nil {
		log.Printf("ERROR: transfer cleanup: load manifest: %v (skip this round)", err)
		return
	}
	now := time.Now().UTC()
	var kept []transferFile
	var freed int64

	for _, f := range m.Files {
		expired := false
		if f.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, f.ExpiresAt); err == nil && now.After(t) {
				expired = true
			}
		}
		downloadsExhausted := f.MaxDownloads > 0 && f.Downloads >= f.MaxDownloads

		if expired || downloadsExhausted {
			// Remove 失败(非 NotExist)时保留条目:文件仍在盘上,条目删除后
			// 列表看不到、删除接口够不到、配额不再计数——成为无任何回收路径
			// 的孤儿。保留条目让下一轮清理重试,并留 ERROR 痕迹。
			if err := os.Remove(s.filePath(f.Code, f.Filename)); err != nil && !os.IsNotExist(err) {
				log.Printf("ERROR: transfer cleanup remove %s: %v (entry kept for retry)", f.Code, err)
				kept = append(kept, f)
				continue
			}
			freed += f.Size
		} else {
			kept = append(kept, f)
		}
	}

	removed := len(m.Files) - len(kept)
	if removed > 0 {
		m.Files = kept
		m.TotalSize -= freed
		if m.TotalSize < 0 {
			m.TotalSize = 0
		}
		if err := s.saveLocked(m); err != nil {
			log.Printf("ERROR: save transfer manifest (cleanup): %v", err)
		}
	}
}

// StartCleanupLoop 后台每小时清理过期文件
func (s *Store) StartCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.cleanupExpiredTransferFiles()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Store) DeleteUploadsByUploader(uploader string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := s.loadLocked()
	if err != nil {
		return 0, err
	}
	removed := 0
	var kept []transferFile
	for _, f := range m.Files {
		if f.Uploader == uploader {
			if err := os.Remove(s.filePath(f.Code, f.Filename)); err != nil && !os.IsNotExist(err) {
				// 删除失败时保留条目：条目一旦移除，文件既不可见也无法再被
				// 清理（清理只遍历 manifest），会成为永久的无主文件。
				log.Printf("ERROR: transfer cascade delete %s: %v (entry kept for retry)", f.Code, err)
				kept = append(kept, f)
				continue
			}
			removed++
			continue
		}
		kept = append(kept, f)
	}
	if removed == 0 {
		return 0, nil
	}
	m.Files = kept
	// 按保留条目重算总占用，避免逐项减法在部分失败/历史漂移下出现负数。
	m.TotalSize = 0
	for _, f := range kept {
		m.TotalSize += f.Size
	}
	if err := s.saveLocked(m); err != nil {
		return removed, err
	}
	log.Printf("[security] transfer cascade delete uploader=%q removed=%d", uploader, removed)
	return removed, nil
}
