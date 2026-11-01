package identity

import (
	"errors"
	"os"
	"testing"

	"admin/internal/runtime"
)

func newStore(t *testing.T, fixture string, policy Policy) (*Store, runtime.Layout) {
	t.Helper()
	layout := runtime.NewLayout(t.TempDir(), t.TempDir())
	if fixture != "" {
		if err := os.WriteFile(layout.UsersMetaPath(), []byte(fixture), 0600); err != nil {
			t.Fatalf("write users fixture: %v", err)
		}
	}
	return NewStoreWithPolicy(layout, nil, policy), layout
}

func TestReconcileRemovesOrphanHtpasswd(t *testing.T) {
	store, layout := newStore(t, `{"users":{"keep":{"role":"viewer"},"missing":{"role":"viewer"}}}`, Policy{})
	if err := addHtpasswdLocked(layout.HtpasswdPath(), "keep", "password123"); err != nil {
		t.Fatal(err)
	}
	if err := addHtpasswdLocked(layout.HtpasswdPath(), "orphan", "password123"); err != nil {
		t.Fatal(err)
	}
	if err := store.Reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	users, err := readHtpasswdUsers(layout.HtpasswdPath())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, u := range users {
		seen[u] = true
	}
	if seen["orphan"] {
		t.Fatalf("orphan credential was not removed: %v", users)
	}
	if !seen["keep"] {
		t.Fatalf("valid credential was removed: %v", users)
	}
}

func TestUpsertUserPreservesDurableFields(t *testing.T) {
	store, _ := newStore(t, "", Policy{})
	res, err := store.UpsertUser("alice", "password123", "viewer")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if res.Role != "viewer" || !res.Created {
		t.Fatalf("unexpected result: %+v", res)
	}
	proxyPass, err := store.EnsureProxyPassword("alice")
	if err != nil {
		t.Fatalf("ensure proxy: %v", err)
	}
	// 编辑时省略 role：保留原角色与 proxy 密码。
	res, err = store.UpsertUser("alice", "password456", "")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if res.Role != "viewer" || res.Created {
		t.Fatalf("edit should preserve role: %+v", res)
	}
	info, err := store.Lookup("alice")
	if err != nil || info == nil {
		t.Fatalf("lookup: %v %+v", err, info)
	}
	if info.ProxyPassword != proxyPass {
		t.Fatalf("proxy password was reset: %q != %q", info.ProxyPassword, proxyPass)
	}
	if got := store.ProxyAuthForUserRead("alice"); got != "alice:"+proxyPass {
		t.Fatalf("proxy auth = %q", got)
	}
}

func TestUpsertUserRejectsProtectedAccount(t *testing.T) {
	store, _ := newStore(t, `{"users":{"root":{"role":"admin","created_at":"2026-01-01T00:00:00Z"}}}`, Policy{ProtectedUser: "root"})
	if _, err := store.UpsertUser("root", "password123", "admin"); !errors.Is(err, ErrProtectedAccount) {
		t.Fatalf("expected ErrProtectedAccount, got %v", err)
	}
}

func TestUpsertUserRejectsReservedName(t *testing.T) {
	store, _ := newStore(t, "", Policy{})
	if _, err := store.UpsertUser("fail2ban", "password123", "viewer"); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("expected ErrInvalidUsername, got %v", err)
	}
}

func TestDeleteUserIdempotentAndCleansCredentials(t *testing.T) {
	store, layout := newStore(t, "", Policy{})
	if _, err := store.UpsertUser("alice", "password123", "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureProxyPassword("alice"); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteUser("alice")
	if err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	if info, err := store.Lookup("alice"); err != nil || info != nil {
		t.Fatalf("user still present after delete: err=%v info=%+v", err, info)
	}
	users, err := readHtpasswdUsers(layout.HtpasswdPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u == "alice" {
			t.Fatal("htpasswd entry not removed")
		}
	}
	if data, err := os.ReadFile(layout.ProxyHtpasswdPath()); err == nil && len(data) != 0 {
		t.Fatalf("proxy htpasswd should be empty, got %q", data)
	}
	// 重试删除：用户已不在，但仍返回成功（可重入）。
	deleted, err = store.DeleteUser("alice")
	if err != nil || deleted {
		t.Fatalf("retry delete: deleted=%v err=%v", deleted, err)
	}
}

func TestEnsureProxyPasswordRefusesMissingAndReserved(t *testing.T) {
	store, _ := newStore(t, "", Policy{})
	if _, err := store.EnsureProxyPassword("nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	if _, err := store.EnsureProxyPassword("fail2ban"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("reserved user: %v", err)
	}
}

func TestLastAdminPolicy(t *testing.T) {
	store, _ := newStore(t, `{"users":{
		"a":{"role":"admin"},
		"b":{"role":"admin"}
	}}`, Policy{})
	if err := store.SetRole("a", "viewer"); err != nil {
		t.Fatalf("demote one of two admins: %v", err)
	}
	if err := store.SetRole("b", "viewer"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
	if _, err := store.DeleteUser("b"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin on delete, got %v", err)
	}
}

func TestChangePasswordVerifiesOldPassword(t *testing.T) {
	store, layout := newStore(t, "", Policy{})
	if _, err := store.UpsertUser("alice", "password123", "viewer"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.ChangePassword("alice", "wrong-password", "password456", true)
	if err != nil || ok {
		t.Fatalf("wrong old password: ok=%v err=%v", ok, err)
	}
	ok, err = store.ChangePassword("alice", "password123", "password456", true)
	if err != nil || !ok {
		t.Fatalf("correct old password: ok=%v err=%v", ok, err)
	}
	if !verifyHtpasswdFile(layout.HtpasswdPath(), "alice", "password456") {
		t.Fatal("new password not written")
	}
}
