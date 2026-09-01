package main

// 网易云账号扫码登录:管理面板展示二维码,轮询扫码状态,成功后提取
// MUSIC_U cookie 写入共享文件(music_server 热加载,30s 内生效)。
// 上游是 netease_api 容器(NETEASE_API_BASE);登录态只用于解锁完整音频,
// cookie 不落日志、不返回给前端。

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// neteaseCookiePathDefault 与 music_server 的 NETEASE_COOKIE_FILE 指向同一文件
	// (admin 侧 /app/data = 宿主 .wework,music_server 侧只读挂载)。
	neteaseCookiePathDefault = "/app/data/generated/music/netease-cookie.txt"

	// neteaseLoginSessionTTL 是二维码会话保留时长(网易二维码约 2 分钟有效)。
	neteaseLoginSessionTTL = 3 * time.Minute
)

// neteaseCookiePath var 供测试替换。
var neteaseCookiePath = neteaseCookiePathDefault

// extractMusicU 从扫码成功返回的 cookie 串中提取 MUSIC_U 值。
// 不用正则匹配：cookie 值可能含 _ / - / URL 编码字符（%xx），旧的正则
// `[0-9a-zA-Z]+` 会截断导致登录态不完整（扫码成功但解锁静默失败）。
// 按 cookie 语义解析：分号分段后在段内定位 MUSIC_U= 取值到段尾（兼容
// 段间逗号分隔，如 "Path=/, MUSIC_U=xxx"），再做 URL 解码。
func extractMusicU(cookie string) string {
	const prefix = "MUSIC_U="
	for _, part := range strings.Split(cookie, ";") {
		if idx := strings.Index(part, prefix); idx >= 0 {
			raw := strings.TrimSpace(part[idx+len(prefix):])
			if raw == "" {
				return ""
			}
			if dec, err := url.QueryUnescape(raw); err == nil {
				raw = dec
			}
			return raw
		}
	}
	return ""
}

// neteaseLoginSession 保存一次扫码会话(key 对应的二维码)。
type neteaseLoginSession struct {
	QRImg     string
	CreatedAt time.Time
}

var neteaseLoginMu sync.Mutex

// neteaseLoginSessions key -> session;过期条目惰性清理。
var neteaseLoginSessions = map[string]neteaseLoginSession{}

func neteaseUpstreamBase() string {
	return getEnv("NETEASE_API_BASE")
}

// neteaseUpstreamGet 请求上游并原样返回 JSON body(只透传,不解析细节)。
func neteaseUpstreamGet(path string, timeout time.Duration) ([]byte, error) {
	base := neteaseUpstreamBase()
	if base == "" {
		return nil, fmt.Errorf("netease_api upstream not configured")
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(base + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream %s: %s", path, resp.Status)
	}
	// 上游是内网自有容器,响应体(二维码 dataURL 量级)可信。
	return io.ReadAll(resp.Body)
}

// apiNeteaseLoginStart 生成扫码 key 与二维码。
func apiNeteaseLoginStart() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ts := time.Now().UnixMilli()
		keyBody, err := neteaseUpstreamGet(fmt.Sprintf("/login/qr/key?timestamp=%d", ts), 8*time.Second)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		var keyResp struct {
			Data struct {
				Unikey string `json:"unikey"`
			} `json:"data"`
		}
		if err := json.Unmarshal(keyBody, &keyResp); err != nil || keyResp.Data.Unikey == "" {
			writeError(w, "获取扫码 key 失败", http.StatusBadGateway)
			return
		}
		qrBody, err := neteaseUpstreamGet(
			fmt.Sprintf("/login/qr/create?key=%s&qrimg=true&timestamp=%d", keyResp.Data.Unikey, ts), 8*time.Second)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		var qrResp struct {
			Data struct {
				QRImg string `json:"qrimg"`
			} `json:"data"`
		}
		if err := json.Unmarshal(qrBody, &qrResp); err != nil || qrResp.Data.QRImg == "" {
			writeError(w, "获取二维码失败", http.StatusBadGateway)
			return
		}

		neteaseLoginMu.Lock()
		// 惰性清理过期会话,防止 map 无界增长。
		for k, s := range neteaseLoginSessions {
			if time.Since(s.CreatedAt) > neteaseLoginSessionTTL {
				delete(neteaseLoginSessions, k)
			}
		}
		neteaseLoginSessions[keyResp.Data.Unikey] = neteaseLoginSession{
			QRImg:     qrResp.Data.QRImg,
			CreatedAt: time.Now(),
		}
		neteaseLoginMu.Unlock()

		writeJSON(w, map[string]string{"key": keyResp.Data.Unikey, "qrimg": qrResp.Data.QRImg})
	}
}

// apiNeteaseLoginPoll 轮询扫码状态;803(成功)时提取 MUSIC_U 写入 cookie 文件。
func apiNeteaseLoginPoll() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			writeError(w, "missing key", http.StatusBadRequest)
			return
		}
		neteaseLoginMu.Lock()
		_, known := neteaseLoginSessions[key]
		neteaseLoginMu.Unlock()
		if !known {
			writeError(w, "二维码已过期，请重新生成", http.StatusGone)
			return
		}

		body, err := neteaseUpstreamGet(
			fmt.Sprintf("/login/qr/check?key=%s&timestamp=%d", key, time.Now().UnixMilli()), 8*time.Second)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		var check struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Cookie  string `json:"cookie"`
			Profile struct {
				Nickname string `json:"nickname"`
			} `json:"profile"`
		}
		if err := json.Unmarshal(body, &check); err != nil {
			writeError(w, "上游响应解析失败", http.StatusBadGateway)
			return
		}
		switch check.Code {
		case 801, 802:
			writeJSON(w, map[string]any{"code": check.Code, "message": check.Message})
			return
		case 803:
			musicU := extractMusicU(check.Cookie)
			if musicU == "" {
				log.Printf("netease login 803 without MUSIC_U cookie")
				writeError(w, "登录成功但未取得凭证，请重试", http.StatusBadGateway)
				return
			}
			if err := writeNeteaseCookie("MUSIC_U=" + musicU); err != nil {
				writeInternalError(w, err)
				return
			}
			// 会话一次性,防 key 复用。
			neteaseLoginMu.Lock()
			delete(neteaseLoginSessions, key)
			neteaseLoginMu.Unlock()
			log.Printf("netease login ok, nickname=%s", check.Profile.Nickname)
			writeJSON(w, map[string]any{"code": 803, "nickname": check.Profile.Nickname})
		default:
			writeError(w, fmt.Sprintf("扫码状态异常(%d)", check.Code), http.StatusBadGateway)
		}
	}
}

// apiNeteaseLoginState 返回当前登录态:cookie 文件存在即视为已配置,
// 并经上游 login/status 校验与取昵称(上游失败不影响已配置状态的展示)。
func apiNeteaseLoginState() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := os.Stat(neteaseCookiePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, map[string]any{"loggedIn": false})
				return
			}
			writeInternalError(w, err)
			return
		}
		cookie, err := os.ReadFile(neteaseCookiePath)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		resp := map[string]any{
			"loggedIn":  true,
			"updatedAt": info.ModTime().Format(time.RFC3339),
		}
		if body, err := neteaseUpstreamGetWithCookie(
			fmt.Sprintf("/login/status?timestamp=%d", time.Now().UnixMilli()), string(cookie)); err == nil {
			var status struct {
				Profile struct {
					Nickname string `json:"nickname"`
				} `json:"profile"`
			}
			if json.Unmarshal(body, &status) == nil && status.Profile.Nickname != "" {
				resp["nickname"] = status.Profile.Nickname
			}
		}
		writeJSON(w, resp)
	}
}

// apiNeteaseLoginLogout 删除 cookie 文件(music_server 检测到文件消失即回匿名)。
func apiNeteaseLoginLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := os.Remove(neteaseCookiePath); err != nil && !os.IsNotExist(err) {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// writeNeteaseCookie 原子写入 cookie 文件并收紧权限(凭证文件仅属主可读)。
func writeNeteaseCookie(content string) error {
	if err := os.MkdirAll(filepath.Dir(neteaseCookiePath), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(neteaseCookiePath, []byte(content), 0o600)
}

// neteaseUpstreamGetWithCookie 带 cookie 请求上游(登录态校验用)。
func neteaseUpstreamGetWithCookie(path, cookie string) ([]byte, error) {
	base := neteaseUpstreamBase()
	if base == "" {
		return nil, fmt.Errorf("netease_api upstream not configured")
	}
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookie)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream %s: %s", path, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
