package music

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeNeteaseUpstream 模拟 netease_api 的扫码登录三接口。
type fakeNeteaseUpstream struct {
	srv       *httptest.Server
	scanState atomic.Int32 // 0=未扫码 1=已扫码 2=已确认
	sawCookie atomic.Value
}

func newFakeNeteaseUpstream(t *testing.T) *fakeNeteaseUpstream {
	t.Helper()
	f := &fakeNeteaseUpstream{}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/qr/key", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"unikey":"test-key-1"},"code":200}`)
	})
	mux.HandleFunc("/login/qr/create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"qrimg":"data:image/png;base64,QUJD","qrurl":"https://x"},"code":200}`)
	})
	mux.HandleFunc("/login/qr/check", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch f.scanState.Load() {
		case 0:
			fmt.Fprint(w, `{"code":801,"message":"等待扫码","cookie":"NMTID=abc; Path=/"}`)
		case 1:
			fmt.Fprint(w, `{"code":802,"message":"待确认","cookie":""}`)
		default:
			fmt.Fprint(w, `{"code":803,"message":"授权成功","cookie":"NMTID=abc; Max-Age=1; Expires=Wed, 20 Aug 2036 17:37:15 GMT; Path=/, MUSIC_U=deadbeef123; Max-Age=2592000; Path=/","profile":{"nickname":"测试用户"}}`)
		}
	})
	mux.HandleFunc("/login/status", func(w http.ResponseWriter, r *http.Request) {
		f.sawCookie.Store(r.Header.Get("Cookie"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"code":200},"profile":{"nickname":"测试用户"}}`)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestNeteaseLoginFlow(t *testing.T) {
	f := newFakeNeteaseUpstream(t)
	t.Setenv("NETEASE_API_BASE", f.srv.URL)
	dir := t.TempDir()
	old := neteaseCookiePath
	neteaseCookiePath = dir + "/netease-cookie.txt"
	t.Cleanup(func() { neteaseCookiePath = old })

	// 初始未登录。
	rec := httptest.NewRecorder()
	ApiNeteaseLoginState().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state", nil))
	if !strings.Contains(rec.Body.String(), `"loggedIn":false`) {
		t.Fatalf("expect logged out initially, got %s", rec.Body.String())
	}

	// start 拿二维码。
	rec = httptest.NewRecorder()
	ApiNeteaseLoginStart().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/start", nil))
	if !strings.Contains(rec.Body.String(), "test-key-1") || !strings.Contains(rec.Body.String(), "base64") {
		t.Fatalf("start: %s", rec.Body.String())
	}

	// 未扫码 801;已扫码 802。
	poll := httptest.NewRequest(http.MethodGet, "/poll?key=test-key-1", nil)
	for _, want := range []string{"801", "802"} {
		rec = httptest.NewRecorder()
		ApiNeteaseLoginPoll().ServeHTTP(rec, poll)
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("poll expect %s, got %s", want, rec.Body.String())
		}
		f.scanState.Add(1)
	}

	// 确认 803:写 cookie 文件,只存 MUSIC_U。
	rec = httptest.NewRecorder()
	ApiNeteaseLoginPoll().ServeHTTP(rec, poll)
	if !strings.Contains(rec.Body.String(), `"code":803`) || !strings.Contains(rec.Body.String(), "测试用户") {
		t.Fatalf("poll 803: %s", rec.Body.String())
	}
	data, err := os.ReadFile(neteaseCookiePath)
	if err != nil || string(data) != "MUSIC_U=deadbeef123" {
		t.Fatalf("cookie file: %q err=%v", data, err)
	}

	// state:已登录 + 昵称(带 cookie 校验上游)。
	rec = httptest.NewRecorder()
	ApiNeteaseLoginState().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state", nil))
	var st struct {
		Data struct {
			LoggedIn bool   `json:"loggedIn"`
			Nickname string `json:"nickname"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Data.LoggedIn || st.Data.Nickname != "测试用户" {
		t.Fatalf("state: %s", rec.Body.String())
	}
	if f.sawCookie.Load() != "MUSIC_U=deadbeef123" {
		t.Fatalf("state should verify with cookie, saw %v", f.sawCookie.Load())
	}

	// key 一次性:803 后再 poll 报过期。
	rec = httptest.NewRecorder()
	ApiNeteaseLoginPoll().ServeHTTP(rec, poll)
	if rec.Code != http.StatusGone {
		t.Fatalf("expect 410 for reused key, got %d", rec.Code)
	}

	// 登出:删文件回未登录。
	rec = httptest.NewRecorder()
	ApiNeteaseLoginLogout().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/logout", nil))
	rec = httptest.NewRecorder()
	ApiNeteaseLoginState().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state", nil))
	if !strings.Contains(rec.Body.String(), `"loggedIn":false`) {
		t.Fatalf("expect logged out after logout, got %s", rec.Body.String())
	}
}
