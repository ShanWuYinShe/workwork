package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// musicUploadRequest 构造多文件上传请求体。
func musicUploadRequest(t *testing.T, files map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, content := range files {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/music-library/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestMusicLibraryUploadListDelete(t *testing.T) {
	dir := t.TempDir()
	old := musicLibraryDir
	musicLibraryDir = dir
	t.Cleanup(func() { musicLibraryDir = old })

	// 上传:合法音频入库;非白名单扩展跳过。
	// 注:Go mime/multipart 标准库已自动对 filename 取 basename(CVE 修复),
	// "../evil.wav"/"sub/dir/track.flac" 到达 handler 时已是 "evil.wav"/
	// "track.flac"——洗白后落库内,不会写到库外(下方 Stat 验证)。
	req := musicUploadRequest(t, map[string]string{
		"歌手A - 歌名B.mp3":      "audio-bytes",
		"../evil.wav":        "x",
		"sub/dir/track.flac": "y",
		"notes.txt":          "z",
	})
	rec := httptest.NewRecorder()
	apiMusicLibraryUpload().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var up struct {
		Data struct {
			Saved   []string `json:"saved"`
			Skipped []string `json:"skipped"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if len(up.Data.Saved) != 3 || up.Data.Saved[0] != "歌手A - 歌名B.mp3" {
		t.Fatalf("expect 3 audio files saved, got %s", rec.Body.String())
	}
	if len(up.Data.Skipped) != 1 {
		t.Fatalf("expect only notes.txt skipped, got %s", rec.Body.String())
	}
	// 穿越目标名不能出现在库目录之外(标准库 Base 化保证)。
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.wav")); !os.IsNotExist(err) {
		t.Fatal("traversal file must not land outside library")
	}

	// 列表:只见白名单音频。
	rec = httptest.NewRecorder()
	apiMusicLibraryList().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/music-library", nil))
	var list struct {
		Data struct {
			Files []musicLibraryFile `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data.Files) != 3 {
		t.Fatalf("unexpected list: %s", rec.Body.String())
	}

	// 删除:合法名成功,穿越名拒绝。
	rec = httptest.NewRecorder()
	apiMusicLibraryDelete().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete,
		"/api/admin/music-library?name="+url.QueryEscape("歌手A - 歌名B.mp3"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	apiMusicLibraryDelete().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete,
		"/api/admin/music-library?name="+url.QueryEscape("../../etc/passwd"), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal delete must be rejected, got %d", rec.Code)
	}
}

// 同名覆盖:重传同名文件原子替换,无临时文件残留。
func TestMusicLibraryUploadOverwrites(t *testing.T) {
	dir := t.TempDir()
	old := musicLibraryDir
	musicLibraryDir = dir
	t.Cleanup(func() { musicLibraryDir = old })

	for i, content := range []string{"v1", "v2-longer"} {
		req := musicUploadRequest(t, map[string]string{"同一首 - 名字.mp3": content})
		rec := httptest.NewRecorder()
		apiMusicLibraryUpload().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("upload #%d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "同一首 - 名字.mp3"))
	if err != nil || string(data) != "v2-longer" {
		t.Fatalf("expect overwritten content, got %q err=%v", data, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expect single file, no tmp residue, got %d", len(entries))
	}
}
