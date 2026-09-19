package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePersistedConfig 写一份最小合法的持久化配置(指定 revision)。
func writePersistedConfig(t *testing.T, revision int64) {
	t.Helper()
	cfg := map[string]any{
		"service":    map[string]any{"upstream": "media_server:8787", "public_path": "/api/media"},
		"sources":    []map[string]any{{"id": "bilibili", "title": "B", "description": "d", "riskLevel": "low", "enabled": true}},
		"categories": []map[string]any{{"id": "c1", "title": "C1", "description": "d", "keywords": []string{"k"}}},
		"revision":   revision,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestApiSaveMediaCenterConfigConflict 乐观锁:PUT 携带的 revision 与持久化
// 不一致时返回 409 CONFIG_CONFLICT,且不落盘不触发重启。
func TestApiSaveMediaCenterConfigConflict(t *testing.T) {
	dir := t.TempDir()
	oldConfig, oldCategories := ConfigPath, CategoriesPath
	ConfigPath = filepath.Join(dir, "config.json")
	CategoriesPath = filepath.Join(dir, "categories.json")
	t.Cleanup(func() { ConfigPath, CategoriesPath = oldConfig, oldCategories })

	writePersistedConfig(t, 5)

	body := `{
		"service": {"upstream": "media_server:8787", "public_path": "/api/media"},
		"sources": [{"id": "bilibili", "title": "B", "description": "d", "riskLevel": "low", "enabled": true}],
		"categories": [{"id": "c1", "title": "C1", "description": "d", "keywords": ["k"]}],
		"revision": 4
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/admin/media-center/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	ApiSaveMediaCenterConfig(nil)(rec, req) // 409 路径在 docker cli 使用之前返回

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ErrorCode != "CONFIG_CONFLICT" {
		t.Fatalf("error_code = %q, want CONFIG_CONFLICT", resp.ErrorCode)
	}
	// 持久化文件未被改动(revision 仍为 5)。
	persisted, err := os.ReadFile(ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var persistedCfg struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(persisted, &persistedCfg); err != nil {
		t.Fatal(err)
	}
	if persistedCfg.Revision != 5 {
		t.Fatalf("persisted revision = %d, want 5 (untouched)", persistedCfg.Revision)
	}
}

// TestMediaCenterConfigRevisionRoundTrip revision 字段经 normalize 透传,
// GET→PUT 往返不丢失。
func TestMediaCenterConfigRevisionRoundTrip(t *testing.T) {
	in := mediaCenterConfig{
		Revision: 7,
		Sources: []mediaCenterSource{
			{ID: "bilibili", Title: "B", Description: "d", RiskLevel: "low", Enabled: true},
		},
		Categories: []mediaCenterCategory{
			{ID: "c1", Title: "C1", Description: "d", Keywords: []string{"k"}},
		},
	}
	out, err := normalizeMediaCenterConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != 7 {
		t.Fatalf("revision = %d, want 7 (must survive normalize)", out.Revision)
	}
}
