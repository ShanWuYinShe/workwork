package downloads

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"admin/internal/platform"
)

// TestHandleToolDownloadAlreadyExistsUsesNDJSON 锁定"已存在"分支的响应协议。
// 前端对 /download 逐行解析 NDJSON 事件且只认 {type:...}:该分支若回的是纯 JSON
// 对象(修复前行为),浏览器端会抛 "Download completed but no result received",
// already_exists 分支永远不可达,用户看到的是误导性失败而不是"已下载过"。
func TestHandleToolDownloadAlreadyExistsUsesNDJSON(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "downloads.json")
	stored := storedDownload{
		Version:  "1.0.0",
		Plat:     "linux-x64",
		Filename: "claude-1.0.0-linux-x64.tar.gz",
	}
	if err := platform.SaveJSONFile(manifestPath, storedManifest{Downloads: []storedDownload{stored}}); err != nil {
		t.Fatalf("准备 manifest 失败: %v", err)
	}

	s := &downloadService{
		platforms:      []downloadPlatform{{Label: "Linux x64", Plat: "linux-x64"}},
		parseAssetPlat: func(string) string { return "linux-x64" },
		buildFilename:  func(version, plat, _ string) string { return "claude-" + version + "-" + plat + ".tar.gz" },
		manifestPath:   manifestPath,
	}
	asset := ghAsset{
		Name:               "claude-1.0.0-linux-x64.tar.gz",
		BrowserDownloadURL: "https://example.invalid/claude-1.0.0-linux-x64.tar.gz",
	}
	s.cache.releases = []releaseInfo{{Version: "1.0.0", Assets: []ghAsset{asset}}}

	req := httptest.NewRequest("POST", "/api/admin/claude-code/download", strings.NewReader(`{"version":"1.0.0","plat":"linux-x64"}`))
	rec := httptest.NewRecorder()
	handleToolDownload(s)(rec, req)

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/x-ndjson") {
		t.Fatalf("Content-Type = %q, want application/x-ndjson(客户端按 NDJSON 逐行解析); body=%q", got, rec.Body.String())
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rec.Body.String())), &ev); err != nil {
		t.Fatalf("响应不是合法 NDJSON 事件: %v (body=%q)", err, rec.Body.String())
	}
	if ev["type"] != "done" || ev["status"] != "already_exists" || ev["filename"] != stored.Filename {
		t.Fatalf("事件 = %v, want {type:done status:already_exists filename:%s}", ev, stored.Filename)
	}
}
