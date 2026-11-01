package xray

import (
	"errors"
	"os"
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
	if err := l.Add(Client{ID: "uuid-a1", Owner: "alice", Email: "alice"}); err != nil {
		t.Fatalf("add first: %v", err)
	}
	if err := l.Add(Client{ID: "uuid-a2", Owner: "alice", Email: "alice"}); err != nil {
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
		if c.Email != c.ID {
			t.Fatalf("email must equal client ID: %+v", c)
		}
		if seen[c.Email] {
			t.Fatalf("duplicate email: %+v", clients)
		}
		seen[c.Email] = true
	}
	if err := l.Add(Client{ID: "UUID-A1", Owner: "bob"}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("case-insensitive duplicate ID: %v", err)
	}
}

func TestLedgerEnsureNormalizesLegacy(t *testing.T) {
	l, layout := newLedger(t)
	legacy := `[
	  {"id":"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE","owner":"alice","email":"shared"},
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
	for _, c := range clients {
		if c.ID != lower(c.ID) || c.Email != c.ID {
			t.Fatalf("entry not normalized: %+v", c)
		}
	}
	aliases := l.Aliases()
	if aliases["bob"] != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("bob alias missing: %+v", aliases)
	}
	if _, ok := aliases["shared"]; ok {
		t.Fatalf("ambiguous email must not alias: %+v", aliases)
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
	_ = l.Add(Client{ID: "uuid-alice", Owner: "alice"})
	_ = l.Add(Client{ID: "uuid-bob", Owner: "bob"})

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
			if err := l.Add(Client{ID: id, Owner: "alice"}); err != nil {
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
