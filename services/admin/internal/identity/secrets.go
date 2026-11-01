package identity

import (
	"admin/internal/platform"
	"admin/internal/runtime"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
)

// internalTokenPattern 是 token 的字符集约束：它会被渲染进 nginx map 正则，
// 必须排除引号、反斜杠、$、空白等会破坏/注入配置的字符。
var internalTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// ValidInternalToken 是 token 字符集的唯一校验入口（nginx 渲染与启动引导共用）。
func ValidInternalToken(tok string) bool {
	return internalTokenPattern.MatchString(tok)
}

// Secrets 管理 internal token 与 fail2ban token 两份共享密钥。
// 值同时缓存在内存（atomic）与磁盘（0640），启动时确保一致。
type Secrets struct {
	layout   runtime.Layout
	internal atomic.Value
	fail2ban atomic.Value
}

func NewSecrets(layout runtime.Layout) *Secrets {
	return &Secrets{layout: layout}
}

func (s *Secrets) InternalToken() string {
	v, _ := s.internal.Load().(string)
	return v
}

func (s *Secrets) Fail2BanToken() string {
	v, _ := s.fail2ban.Load().(string)
	return v
}

func (s *Secrets) EnsureInternalToken() error {
	tok, err := s.ensure(s.layout.InternalTokenPath(), "INTERNAL_TOKEN", true)
	if err != nil {
		return err
	}
	s.internal.Store(tok)
	return nil
}

func (s *Secrets) EnsureFail2BanToken() error {
	tok, err := s.ensure(s.layout.Fail2BanTokenPath(), "FAIL2BAN_TOKEN", false)
	if err != nil {
		return err
	}
	s.fail2ban.Store(tok)
	return nil
}

// ensure 是内/外 token 的唯一实现：env 优先；其次已有文件；否则生成 16
// 字节 hex。写入前校验字符集（仅 internal token），并原子落盘 0640。
func (s *Secrets) ensure(path, envKey string, validate bool) (string, error) {
	if tok := platform.GetEnv(envKey); tok != "" {
		if validate && !ValidInternalToken(tok) {
			return "", fmt.Errorf("%s contains characters outside [A-Za-z0-9._~-]; it is rendered into the nginx config and must not contain quotes, $, backslashes or whitespace", envKey)
		}
		if existing, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(existing)) == tok {
			return tok, platform.EnsurePerm(path, 0640)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", err
		}
		if err := runtime.AtomicWriteFile(path, []byte(tok), 0640); err != nil {
			return "", err
		}
		return tok, nil
	}
	if existing, err := os.ReadFile(path); err == nil {
		tok := strings.TrimSpace(string(existing))
		if tok != "" {
			if validate && !ValidInternalToken(tok) {
				return "", fmt.Errorf("existing token %s contains characters outside [A-Za-z0-9._~-]; delete it to regenerate, or set %s explicitly", path, envKey)
			}
			return tok, platform.EnsurePerm(path, 0640)
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := runtime.AtomicWriteFile(path, []byte(tok), 0640); err != nil {
		return "", err
	}
	return tok, nil
}
