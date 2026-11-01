package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type testDoc struct {
	Values []string `json:"values"`
}

func TestJSONFileMissingKeepsZero(t *testing.T) {
	f := NewJSONFile[testDoc](filepath.Join(t.TempDir(), "absent.json"), 0600, nil)
	got, err := f.Load()
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(got.Values) != 0 {
		t.Fatalf("missing file must keep zero value, got %+v", got)
	}
}

func TestJSONFileCorruptQuarantinesAndNotifies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	var events []string
	f := NewJSONFile[testDoc](path, 0600, NotifyFunc(func(event, _ string, _ map[string]string) {
		events = append(events, event)
	}))
	if _, err := f.Load(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt file must return ErrCorrupt, got %v", err)
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("corrupt file should be quarantined: %v", err)
	}
	if len(events) != 1 || events[0] != "data.corrupt" {
		t.Fatalf("expected one data.corrupt notification, got %v", events)
	}
}

func TestJSONFileSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "doc.json")
	f := NewJSONFile[testDoc](path, 0600, nil)
	want := testDoc{Values: []string{"a", "b"}}
	if err := f.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := f.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Values) != 2 || got.Values[0] != "a" || got.Values[1] != "b" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("perm = %o, want 0600", info.Mode().Perm())
	}
}
