package xray

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"admin/internal/runtime"
)

func newLedger(t *testing.T) (*Ledger, runtime.Layout) {
	t.Helper()
	layout := runtime.NewLayout(t.TempDir(), t.TempDir())
	return NewLedger(layout, nil), layout
}

func TestLedgerAddMultiplePerOwner(t *testing.T) {
	l, _ := newLedger(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Add(Client{ID: "uuid-a1", Owner: "alice"}); err != nil {
		t.Fatalf("add first: %v", err)
	}
	if _, err := l.Add(Client{ID: "uuid-a2", Owner: "alice"}); err != nil {
		t.Fatalf("add second: %v", err)
	}
	clients, err := l.Clients()
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 2 {
		t.Fatalf("expected 2 clients, got %+v", clients)
	}
	seen := map[string]bool{}
	for _, c := range clients {
		if !ValidClientName(c.Name) {
			t.Fatalf("client name invalid: %+v", c)
		}
		if seen[c.Name] {
			t.Fatalf("duplicate client name: %+v", clients)
		}
		seen[c.Name] = true
	}
	if clients[0].Name != "alice" || clients[1].Name != "alice-2" {
		t.Fatalf("expected generated names alice/alice-2, got %+v", clients)
	}
	if _, err := l.Add(Client{ID: "UUID-A1", Owner: "bob"}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("case-insensitive duplicate ID: %v", err)
	}
}

func TestLedgerEnsureNormalizesLegacy(t *testing.T) {
	l, layout := newLedger(t)
	// 旧台账：email=UUID 的条目应按 owner 生成用户名；自定义 email 保留；
	// 同一 UUID 的大小写变体去重。
	legacy := `[
	  {"id":"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE","owner":"alice","email":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
	  {"id":"11111111-2222-3333-4444-555555555555","owner":"bob","email":"bob"},
	  {"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","owner":"carol","email":"shared"}
	]`
	if err := os.WriteFile(layout.XrayClientsPath(), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	clients, err := l.Clients()
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 2 {
		t.Fatalf("duplicate UUID variant should be dropped: %+v", clients)
	}
	if clients[0].Name != "alice" || clients[1].Name != "bob" {
		t.Fatalf("legacy names not migrated by owner/custom email: %+v", clients)
	}
	for _, c := range clients {
		if c.ID != lower(c.ID) || c.LegacyEmail != "" {
			t.Fatalf("entry not normalized: %+v", c)
		}
	}
	aliases := l.Aliases()
	if aliases["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"] != "alice" {
		t.Fatalf("UUID email should alias to generated name: %+v", aliases)
	}
	// 迁移后的台账只保留 name，不再写 legacy email 字段。
	data, err := os.ReadFile(layout.XrayClientsPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"email"`) || !strings.Contains(string(data), `"name"`) {
		t.Fatalf("normalized ledger must persist name and drop email: %s", data)
	}
	if _, ok := aliases["shared"]; ok {
		t.Fatalf("ambiguous/dropped email must not alias: %+v", aliases)
	}
}

func lower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}

func TestLedgerDeleteOwnerFilter(t *testing.T) {
	l, _ := newLedger(t)
	_, _ = l.Add(Client{ID: "uuid-alice", Owner: "alice"})
	_, _ = l.Add(Client{ID: "uuid-bob", Owner: "bob"})

	if _, err := l.Delete("uuid-alice", "bob"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-owner delete: %v", err)
	}
	if _, err := l.Delete("missing", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
	if deleted, err := l.Delete("uuid-alice", ""); err != nil || deleted.Owner != "alice" {
		t.Fatalf("admin delete: %+v %v", deleted, err)
	}
	removed, err := l.DeleteByOwner("bob")
	if err != nil || removed != 1 {
		t.Fatalf("delete by owner: removed=%d err=%v", removed, err)
	}
}

func TestLedgerConcurrentAdds(t *testing.T) {
	l, _ := newLedger(t)
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "uuid-" + string(rune('a'+i))
			if _, err := l.Add(Client{ID: id, Owner: "alice"}); err != nil {
				t.Errorf("add %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	clients, err := l.Clients()
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != n {
		t.Fatalf("concurrent adds lost entries: got %d want %d", len(clients), n)
	}
}

func TestLedgerEnsureMissingDoesNotCreateFile(t *testing.T) {
	l, layout := newLedger(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.XrayClientsPath()); !os.IsNotExist(err) {
		t.Fatalf("missing ledger should stay absent, stat err=%v", err)
	}
}

func TestLedgerRejectsDuplicateAndInvalidName(t *testing.T) {
	l, _ := newLedger(t)
	if _, err := l.Add(Client{ID: "uuid-a1", Owner: "alice", Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Add(Client{ID: "uuid-a2", Owner: "alice", Name: "ALICE"}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate name must be rejected, got %v", err)
	}
	if _, err := l.Add(Client{ID: "uuid-a3", Owner: "alice", Name: "bad name"}); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("invalid name must be rejected, got %v", err)
	}
	if created, err := l.Add(Client{ID: "uuid-a4", Owner: "Bob"}); err != nil || created.Name != "bob" {
		t.Fatalf("owner should generate lower-case name, got %+v err=%v", created, err)
	}
}

func TestLedgerRenameValidatesOwnershipAndUniqueness(t *testing.T) {
	l, _ := newLedger(t)
	if _, err := l.Add(Client{ID: "uuid-a", Owner: "alice", Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Add(Client{ID: "uuid-b", Owner: "bob", Name: "bob"}); err != nil {
		t.Fatal(err)
	}

	old, updated, err := l.Rename("UUID-A", "alice-main", "")
	if err != nil || old.Name != "alice" || updated.Name != "alice-main" {
		t.Fatalf("rename: old=%+v updated=%+v err=%v", old, updated, err)
	}
	if _, _, err := l.Rename("uuid-a", "BOB", ""); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, _, err := l.Rename("uuid-a", "bad name", ""); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("invalid name: %v", err)
	}
	if _, _, err := l.Rename("uuid-a", "x", "bob"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-owner rename: %v", err)
	}
	if _, _, err := l.Rename("missing", "x", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing rename: %v", err)
	}
	if again, _, err := l.Rename("uuid-a", "alice-main", ""); err != nil || again.Name != "alice-main" {
		t.Fatalf("idempotent rename: %+v err=%v", again, err)
	}
}
