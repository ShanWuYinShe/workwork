package auth

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReconcileAuthStateRemovesOrphanHtpasswd 锁定 users.json ↔ .htpasswd
// 对账：htpasswd 里没有 users.json 条目的"幽灵凭证"必须在启动时清理，
// 否则它能过 nginx basic auth 却永远进不了面板，也没有 API 能删掉。
func TestReconcileAuthStateRemovesOrphanHtpasswd(t *testing.T) {
	dir := t.TempDir()
	WithNginxAuthDir(t, dir)
	usersPath := filepath.Join(dir, "users.json")
	WithUsersMetaPath(t, usersPath)
	if err := os.WriteFile(usersPath, []byte(
		`{"users":{"keep":{"role":"viewer"},"missing":{"role":"viewer"}}}`), 0o600); err != nil {
		t.Fatalf("write users: %v", err)
	}
	if err := HtpasswdAddUser("keep", "password123"); err != nil {
		t.Fatalf("add keep: %v", err)
	}
	if err := HtpasswdAddUser("orphan", "password123"); err != nil {
		t.Fatalf("add orphan: %v", err)
	}

	if err := ReconcileAuthState(); err != nil {
		t.Fatalf("ReconcileAuthState: %v", err)
	}
	users, err := ReadHtpasswdUsers()
	if err != nil {
		t.Fatalf("read htpasswd: %v", err)
	}
	seen := map[string]bool{}
	for _, u := range users {
		seen[u] = true
	}
	if seen["orphan"] {
		t.Fatalf("orphan htpasswd credential was not removed: %v", users)
	}
	if !seen["keep"] {
		t.Fatalf("valid htpasswd credential was removed: %v", users)
	}
}
