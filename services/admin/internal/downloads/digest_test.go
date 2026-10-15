package downloads

// 下载摘要校验测试(GitHub release asset 的 digest 字段)。
// xray 下载面此前没有任何完整性校验(claude 面只有 SHASUMS256.txt),本组测试
// 锁住三件事:digest 解析、比对语义,以及"校验不通过时不落盘、不写 manifest"
// 这一关键顺序——downloadArchive 内部先校验再 rename/manifest。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetSHA256(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"正常 sha256", "sha256:" + strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"大写十六进制视为非法(上游恒为小写)", "sha256:" + strings.Repeat("A", 64), ""},
		{"长度不足", "sha256:" + strings.Repeat("a", 63), ""},
		{"字段缺失", "", ""},
		{"其他算法", "sha512:" + strings.Repeat("a", 128), ""},
		{"无前缀裸摘要", strings.Repeat("a", 64), ""},
	}
	for _, c := range cases {
		if got := assetSHA256(c.in); got != c.want {
			t.Errorf("%s: assetSHA256(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestVerifyDownloadedSHA256(t *testing.T) {
	if err := verifyDownloadedSHA256("", strings.Repeat("b", 64)); err != nil {
		t.Fatalf("上游未给摘要时应跳过校验, got %v", err)
	}
	if err := verifyDownloadedSHA256(strings.Repeat("a", 64), strings.Repeat("A", 64)); err != nil {
		t.Fatalf("同值不同大小写应通过, got %v", err)
	}
	if err := verifyDownloadedSHA256(strings.Repeat("a", 64), strings.Repeat("b", 64)); !errors.Is(err, errChecksumMismatch) {
		t.Fatalf("摘要不符应返回 errChecksumMismatch, got %v", err)
	}
}

// stubTransport 直接给出响应,绕开 DNS/TLS:测试只关心下载循环与摘要判定。
type stubTransport struct{ body []byte }

func (s stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: int64(len(s.body)),
		Body:          io.NopCloser(bytes.NewReader(s.body)),
		Header:        http.Header{},
		Request:       req,
	}, nil
}

func withStubDownloadClient(t *testing.T, body []byte) {
	t.Helper()
	old := downloadClient
	downloadClient = &http.Client{Transport: stubTransport{body: body}}
	t.Cleanup(func() { downloadClient = old })
}

func newDigestTestService(t *testing.T) *downloadService {
	t.Helper()
	dir := t.TempDir()
	return &downloadService{
		dir:          dir,
		manifestPath: filepath.Join(dir, "downloads.json"),
		platforms:    []downloadPlatform{{Label: "Linux x64", Plat: "linux-64"}},
	}
}

const testAssetURL = "https://github.com/XTLS/Xray-core/releases/download/v1.2.3/Xray-linux-64.zip"

func TestDownloadArchiveAcceptsMatchingDigest(t *testing.T) {
	payload := []byte("fake-xray-zip-bytes")
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])

	s := newDigestTestService(t)
	withStubDownloadClient(t, payload)
	n, got, err := s.downloadArchive(context.Background(), "Xray-1.2.3-linux-64.zip", "v1.2.3", "linux-64", testAssetURL, want, nil)
	if err != nil {
		t.Fatalf("摘要匹配应成功, got %v", err)
	}
	if n != int64(len(payload)) || got != want {
		t.Fatalf("size/checksum = %d/%s, want %d/%s", n, got, len(payload), want)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "Xray-1.2.3-linux-64.zip")); err != nil {
		t.Fatalf("产物未落盘: %v", err)
	}
	stored := s.listStored()
	if len(stored) != 1 || stored[0].Checksum != want {
		t.Fatalf("manifest 条目异常: %+v", stored)
	}
}

func TestDownloadArchiveRejectsDigestMismatch(t *testing.T) {
	payload := []byte("tampered-zip-bytes")
	s := newDigestTestService(t)
	withStubDownloadClient(t, payload)
	_, _, err := s.downloadArchive(context.Background(), "Xray-1.2.3-linux-64.zip", "v1.2.3", "linux-64", testAssetURL, strings.Repeat("0", 64), nil)
	if !errors.Is(err, errChecksumMismatch) {
		t.Fatalf("want errChecksumMismatch, got %v", err)
	}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "Xray-") {
			t.Fatalf("校验失败却留下产物: %s", e.Name())
		}
	}
	if stored := s.listStored(); len(stored) != 0 {
		t.Fatalf("校验失败却写入 manifest: %+v", stored)
	}
}

// handler 级护栏:发布列表里的 digest 必须真的传到 downloadArchive。断掉这条
// 传递链(把 assetSHA256(assetDigest) 换成 "")时本用例会失败——否则"接口在,
// 但没人调用"的静默回归无人发现。
func TestHandleToolDownloadRejectsTamperedAsset(t *testing.T) {
	payload := []byte("tampered-zip-bytes")
	s := newDigestTestService(t)
	s.parseAssetPlat = parseAssetPlatXray
	s.buildFilename = buildXrayFilename
	s.cache.releases = []releaseInfo{{Version: "v1.2.3", Assets: []ghAsset{{
		Name:               "Xray-linux-64.zip",
		BrowserDownloadURL: testAssetURL,
		Digest:             "sha256:" + strings.Repeat("0", 64),
	}}}}
	withStubDownloadClient(t, payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/xray/download", strings.NewReader("{\"version\":\"v1.2.3\",\"plat\":\"linux-64\"}"))
	rec := httptest.NewRecorder()
	handleToolDownload(s)(rec, req)

	if !strings.Contains(rec.Body.String(), "校验失败") {
		t.Fatalf("应返回校验失败事件, got %q", rec.Body.String())
	}
	if stored := s.listStored(); len(stored) != 0 {
		t.Fatalf("校验失败却写入 manifest: %+v", stored)
	}
}
