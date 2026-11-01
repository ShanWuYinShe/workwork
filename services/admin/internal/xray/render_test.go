package xray

import (
	"admin/internal/runtime"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func newRenderLayout(t *testing.T) runtime.Layout {
	t.Helper()
	layout := runtime.NewLayout(t.TempDir(), t.TempDir())
	if err := os.MkdirAll(layout.ConfigDir, 0755); err != nil {
		t.Fatal(err)
	}
	tmpl := `{"inbounds":[{"settings":{"clients":__XRAY_CLIENTS_JSON__}}]}`
	if err := os.WriteFile(layout.XrayConfigTemplatePath(), []byte(tmpl), 0644); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestRenderWritesClientsOnly(t *testing.T) {
	layout := newRenderLayout(t)
	clients := []Client{
		{ID: "a", Owner: "alice", Name: "alice"},
		{ID: "b", Owner: "alice", Name: "alice-2"},
	}
	changed, err := Render(layout, clients)
	if err != nil || !changed {
		t.Fatalf("render: changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(layout.XrayConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Inbounds []struct {
			Settings struct {
				Clients []map[string]any `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("rendered config invalid JSON: %v\n%s", err, data)
	}
	got := doc.Inbounds[0].Settings.Clients
	if len(got) != 2 {
		t.Fatalf("clients = %d, want 2", len(got))
	}
	for _, c := range got {
		if len(c) != 2 || c["id"] == nil || c["email"] == nil {
			t.Fatalf("unexpected client keys (owner/flow must be omitted): %+v", c)
		}
	}
	changed, err = Render(layout, clients)
	if err != nil || changed {
		t.Fatalf("unchanged render: changed=%v err=%v", changed, err)
	}
}

func TestRenderRejectsDuplicateEmail(t *testing.T) {
	layout := newRenderLayout(t)
	if _, err := Render(layout, []Client{{ID: "a", Name: "same"}, {ID: "b", Name: "SAME"}}); err == nil {
		t.Fatal("duplicate email must be rejected before writing")
	}
	if _, err := os.Stat(layout.XrayConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("failed render must not write config, stat err=%v", err)
	}
}

func TestServiceAddRollsBackOnRestartFailure(t *testing.T) {
	layout := newRenderLayout(t)
	svc := NewService(layout, nil, RestarterFunc(func(context.Context) error {
		return errors.New("boom")
	}))
	_, err := svc.Add(context.Background(), Client{ID: "a", Owner: "alice"})
	if !errors.Is(err, ErrRestart) {
		t.Fatalf("expected ErrRestart, got %v", err)
	}
	clients, err := svc.Clients()
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 0 {
		t.Fatalf("ledger not rolled back: %+v", clients)
	}
}

func TestServiceDeleteRollsBackOnRestartFailure(t *testing.T) {
	layout := newRenderLayout(t)
	tmpl := `{"inbounds":[{"settings":{"clients":__XRAY_CLIENTS_JSON__}}]}`
	if err := os.WriteFile(layout.XrayConfigTemplatePath(), []byte(tmpl), 0644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(layout, nil, nil)
	if _, err := svc.Add(context.Background(), Client{ID: "a", Owner: "alice"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc = NewService(layout, nil, RestarterFunc(func(context.Context) error {
		return errors.New("boom")
	}))
	if _, err := svc.Delete(context.Background(), "a", ""); !errors.Is(err, ErrRestart) {
		t.Fatalf("expected ErrRestart, got %v", err)
	}
	clients, err := svc.Clients()
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].ID != "a" {
		t.Fatalf("deleted client not rolled back: %+v", clients)
	}
}

func TestServiceRenameRollsBackOnRestartFailure(t *testing.T) {
	layout := newRenderLayout(t)
	calls := 0
	svc := NewService(layout, nil, RestarterFunc(func(context.Context) error {
		calls++
		if calls > 1 {
			return errors.New("boom")
		}
		return nil
	}))
	if _, err := svc.Add(context.Background(), Client{ID: "a", Owner: "alice", Name: "alice"}); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	if _, _, err := svc.Rename(context.Background(), "a", "alice-main", ""); !errors.Is(err, ErrRestart) {
		t.Fatalf("expected ErrRestart, got %v", err)
	}
	clients, err := svc.Clients()
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].Name != "alice" {
		t.Fatalf("rename not rolled back: %+v", clients)
	}
}
