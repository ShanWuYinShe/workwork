package music

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// chunkPostRequest 构造单片上传请求。chunkSHA 为空串时省略字段(测非法)。
func chunkPostRequest(t *testing.T, uploadID string, index, total int, fileName, chunkSHA string, chunk []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range map[string]string{
		"uploadId": uploadID,
		"index":    fmt.Sprint(index),
		"total":    fmt.Sprint(total),
		"fileName": fileName,
		"sha256":   chunkSHA,
	} {
		if v == "" {
			continue
		}
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := w.CreateFormFile("chunk", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(chunk); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/music-library/upload-chunk", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func chunkRespJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json %s: %v", rec.Body.String(), err)
	}
	return out.Data
}

func setupChunkDirs(t *testing.T) (libDir, chunksDir string) {
	t.Helper()
	libDir = t.TempDir()
	chunksDir = t.TempDir()
	oldLib, oldChunks := musicLibraryDir, uploadChunksDir
	musicLibraryDir, uploadChunksDir = libDir, chunksDir
	t.Cleanup(func() { musicLibraryDir, uploadChunksDir = oldLib, oldChunks })
	return libDir, chunksDir
}

// 全流程:多片乱序上传 → 收齐自动合并入库 → 分片目录清理 → 重复片幂等。
func TestMusicChunkUploadMerge(t *testing.T) {
	libDir, chunksDir := setupChunkDirs(t)
	uploadID := sha256Hex([]byte("merge-test"))
	data := bytes.Repeat([]byte{0xAB}, 1000) // 3 片:500/500/0 附近切
	total := 3
	parts := [][]byte{data[:400], data[400:900], data[900:]}

	upload := ApiMusicChunkUpload()
	var last map[string]any
	// 乱序:先传第 2 片再 1、3,验证 received 累计与收齐合并不依赖顺序。
	for _, idx := range []int{1, 0, 2} {
		req := chunkPostRequest(t, uploadID, idx, total, "大文件 - 分片.flac", sha256Hex(parts[idx]), parts[idx])
		rec := httptest.NewRecorder()
		upload.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("chunk %d: %d %s", idx, rec.Code, rec.Body.String())
		}
		last = chunkRespJSON(t, rec)
	}
	if last["complete"] != true || last["saved"] != "大文件 - 分片.flac" {
		t.Fatalf("expect complete merge, got %v", last)
	}
	got, err := os.ReadFile(filepath.Join(libDir, "大文件 - 分片.flac"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("merged content mismatch: err=%v len=%d", err, len(got))
	}
	if entries, _ := os.ReadDir(chunksDir); len(entries) != 0 {
		t.Fatalf("chunk dir must be removed after merge, got %d entries", len(entries))
	}

	// 重复片幂等:同一会话重传已收片不报错(received 去重)。
	os.MkdirAll(filepath.Join(chunksDir, uploadID), 0o755)
	writeTestFile(t, filepath.Join(chunksDir, uploadID, "meta.json"),
		`{"fileName":"大文件 - 分片.flac","total":2}`)
	writeTestFile(t, filepath.Join(chunksDir, uploadID, "000000.part"), "x")
	req := chunkPostRequest(t, uploadID, 0, 2, "大文件 - 分片.flac", sha256Hex([]byte("x")), []byte("x"))
	rec := httptest.NewRecorder()
	upload.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent re-upload: %d %s", rec.Code, rec.Body.String())
	}
	if data := chunkRespJSON(t, rec); fmt.Sprint(data["received"]) != "[0]" {
		t.Fatalf("received must be deduped, got %v", data["received"])
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 续传查询:无会话返回空;有会话返回 received/total/fileName。
func TestMusicChunkQuery(t *testing.T) {
	_, chunksDir := setupChunkDirs(t)
	uploadID := sha256Hex([]byte("query-test"))
	query := ApiMusicChunkQuery()

	rec := httptest.NewRecorder()
	query.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/music-library/upload-chunk?uploadId="+uploadID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty query: %d", rec.Code)
	}
	if data := chunkRespJSON(t, rec); fmt.Sprint(data["received"]) != "[]" {
		t.Fatalf("expect empty received, got %v", data["received"])
	}

	os.MkdirAll(filepath.Join(chunksDir, uploadID), 0o755)
	writeTestFile(t, filepath.Join(chunksDir, uploadID, "meta.json"), `{"fileName":"a.mp3","total":5}`)
	writeTestFile(t, filepath.Join(chunksDir, uploadID, "000002.part"), "x")
	writeTestFile(t, filepath.Join(chunksDir, uploadID, "000000.part"), "x")
	rec = httptest.NewRecorder()
	query.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/music-library/upload-chunk?uploadId="+uploadID, nil))
	data := chunkRespJSON(t, rec)
	if fmt.Sprint(data["received"]) != "[0 2]" || data["total"] != float64(5) || data["fileName"] != "a.mp3" {
		t.Fatalf("unexpected query: %v", data)
	}
}

// 校验拒绝:非法 uploadId / 越界序号 / 超片数 / 坏文件名 / sha 不符 / 超大分片。
func TestMusicChunkUploadRejects(t *testing.T) {
	setupChunkDirs(t)
	upload := ApiMusicChunkUpload()
	validID := sha256Hex([]byte("reject-test"))
	cases := []struct {
		name    string
		reqFunc func() *http.Request
		code    int
	}{
		{"uploadId 穿越目录", func() *http.Request {
			return chunkPostRequest(t, "../../etc", 0, 2, "a.mp3", sha256Hex([]byte("x")), []byte("x"))
		}, http.StatusBadRequest},
		{"uploadId 非 hex", func() *http.Request {
			return chunkPostRequest(t, "zz"+validID[2:], 0, 2, "a.mp3", sha256Hex([]byte("x")), []byte("x"))
		}, http.StatusBadRequest},
		{"index 越界", func() *http.Request {
			return chunkPostRequest(t, validID, 2, 2, "a.mp3", sha256Hex([]byte("x")), []byte("x"))
		}, http.StatusBadRequest},
		{"total 超限", func() *http.Request {
			return chunkPostRequest(t, validID, 0, 33, "a.mp3", sha256Hex([]byte("x")), []byte("x"))
		}, http.StatusBadRequest},
		{"文件名带路径", func() *http.Request {
			return chunkPostRequest(t, validID, 0, 2, "../evil.mp3", sha256Hex([]byte("x")), []byte("x"))
		}, http.StatusBadRequest},
		{"非音频扩展名", func() *http.Request {
			return chunkPostRequest(t, validID, 0, 2, "a.txt", sha256Hex([]byte("x")), []byte("x"))
		}, http.StatusBadRequest},
		{"sha 不符", func() *http.Request {
			return chunkPostRequest(t, validID, 0, 2, "a.mp3", sha256Hex([]byte("wrong")), []byte("x"))
		}, http.StatusConflict},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		upload.ServeHTTP(rec, tc.reqFunc())
		if rec.Code != tc.code {
			t.Fatalf("%s: expect %d, got %d %s", tc.name, tc.code, rec.Code, rec.Body.String())
		}
	}
	// sha 不符时不能留下分片文件。
	if entries, _ := os.ReadDir(filepath.Join(uploadChunksDir, validID)); len(entries) != 1 { // 仅 meta.json
		t.Fatalf("failed chunk must not persist, got %d entries", len(entries))
	}
}

// meta 不一致(文件修改后同 id 重传)时首片隐式 reset 旧会话。
func TestMusicChunkUploadResetsStaleMeta(t *testing.T) {
	_, chunksDir := setupChunkDirs(t)
	uploadID := sha256Hex([]byte("reset-test"))
	dir := filepath.Join(chunksDir, uploadID)
	os.MkdirAll(dir, 0o755)
	writeTestFile(t, filepath.Join(dir, "meta.json"), `{"fileName":"旧版.mp3","total":9}`)
	writeTestFile(t, filepath.Join(dir, "000007.part"), "stale")

	upload := ApiMusicChunkUpload()
	req := chunkPostRequest(t, uploadID, 0, 2, "新版.mp3", sha256Hex([]byte("v2")), []byte("v2"))
	rec := httptest.NewRecorder()
	upload.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset+upload: %d %s", rec.Code, rec.Body.String())
	}
	if data := chunkRespJSON(t, rec); data["total"] != float64(2) {
		t.Fatalf("stale chunks must be reset, got %v", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "000007.part")); !os.IsNotExist(err) {
		t.Fatal("stale part must be removed")
	}
}

// 过期清理:TTL 之外的会话目录删除,新会话保留,非会话内容不动。
func TestCleanupExpiredChunkUploads(t *testing.T) {
	_, chunksDir := setupChunkDirs(t)
	expired := sha256Hex([]byte("expired-session"))
	os.MkdirAll(filepath.Join(chunksDir, expired), 0o755)
	fresh := filepath.Join(chunksDir, sha256Hex([]byte("fresh-session")))
	os.MkdirAll(fresh, 0o755)
	writeTestFile(t, filepath.Join(fresh, "meta.json"), `{"fileName":"a.mp3","total":2}`)
	// 非 hex 目录名不被触碰(不误删未知内容)。
	weird := filepath.Join(chunksDir, "not-a-session")
	os.MkdirAll(weird, 0o755)

	past := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(filepath.Join(chunksDir, expired), past, past); err != nil {
		t.Fatal(err)
	}
	CleanupExpiredChunkUploads()

	if _, err := os.Stat(filepath.Join(chunksDir, expired)); !os.IsNotExist(err) {
		t.Fatal("expired session must be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh session must survive")
	}
	if _, err := os.Stat(weird); err != nil {
		t.Fatal("non-session dirs must be untouched")
	}
}
