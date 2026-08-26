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
	if len(up.Data.Saved) != 3 {
		t.Fatalf("expect 3 audio files saved, got %s", rec.Body.String())
	}
	// map 遍历顺序随机,saved 顺序不定,按集合断言。
	savedSet := map[string]bool{}
	for _, n := range up.Data.Saved {
		savedSet[n] = true
	}
	for _, want := range []string{"歌手A - 歌名B.mp3", "evil.wav", "track.flac"} {
		if !savedSet[want] {
			t.Fatalf("expect %q in saved, got %s", want, rec.Body.String())
		}
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

// 回归:music-library 上传必须豁免 main 的全局 1MB body 上限,否则超过 1MB
// 的真实歌曲在 ParseMultipartForm 即失败(线上 400"上传内容过大或格式不正确")。
// 走 applyBodyLimit + handler 完整链路 + 真 HTTP 客户端,模拟线上请求路径。
func TestMusicLibraryUploadBypassesGlobalBodyLimit(t *testing.T) {
	if !bodyLimitExempt("/api/admin/music-library/upload") {
		t.Fatal("music-library/upload must be exempt from the global 1MB body limit")
	}

	dir := t.TempDir()
	old := musicLibraryDir
	musicLibraryDir = dir
	t.Cleanup(func() { musicLibraryDir = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applyBodyLimit(w, r)
		apiMusicLibraryUpload().ServeHTTP(w, r)
	}))
	defer srv.Close()

	// 3MB 内容,multipart 编码后必然超过 1MB 全局上限。
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "大文件 - 回归测试.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("a"), 3<<20)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/admin/music-library/upload", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("3MB upload through global body limit chain: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "大文件 - 回归测试.mp3")); err != nil {
		t.Fatalf("file must land in library: %v", err)
	}
}
