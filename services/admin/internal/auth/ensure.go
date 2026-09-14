package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"admin/internal/platform"
)

func EnsureHtpasswd() error {
	if err := os.MkdirAll(NginxAuthDir, 0755); err != nil {
		return err
	}
	if data, err := os.ReadFile(HtpasswdFile); err == nil {
		if len(data) > 10 {
			return platform.EnsurePerm(HtpasswdFile, 0644)
		}
	}
	if data, err := os.ReadFile(legacyHtpasswdPath); err == nil && len(data) > 10 {
		return platform.AtomicWriteFile(HtpasswdFile, data, 0644)
	}
	user := platform.GetEnv("AUTH_USER")
	pass := platform.GetEnv("AUTH_PASS")
	if user == "" || pass == "" {
		return fmt.Errorf("AUTH_USER and AUTH_PASS are required when no existing htpasswd is available")
	}
	out, err := bcryptHtpasswdLine(user, pass)
	if err != nil {
		return err
	}
	return platform.AtomicWriteFile(HtpasswdFile, []byte(out), 0644)
}

func SeedAdminUser() error {
	adminUser := platform.GetEnv("AUTH_USER")
	if adminUser == "" {
		return nil
	}
	return UpdateUsersMeta(func(doc *UsersMetaDoc) (bool, error) {
		if _, exists := doc.Users[adminUser]; !exists {
			doc.Users[adminUser] = UserInfo{Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
			return true, nil
		}
		return false, nil
	})
}

func EnsureInternalToken() error {
	if tok := platform.GetEnv("INTERNAL_TOKEN"); tok != "" {
		if existing, err := os.ReadFile(InternalTokenPath); err == nil && strings.TrimSpace(string(existing)) == tok {
			setInternalToken(tok)
			return platform.EnsurePerm(InternalTokenPath, 0640)
		}
		if err := platform.AtomicWriteFile(InternalTokenPath, []byte(tok), 0640); err != nil {
			return err
		}
		setInternalToken(tok)
		return nil
	}
	if existing, err := os.ReadFile(InternalTokenPath); err == nil {
		tok := strings.TrimSpace(string(existing))
		if tok != "" {
			setInternalToken(tok)
			return platform.EnsurePerm(InternalTokenPath, 0640)
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := hex.EncodeToString(b)
	if err := platform.AtomicWriteFile(InternalTokenPath, []byte(tok), 0640); err != nil {
		return err
	}
	setInternalToken(tok)
	return nil
}

func EnsureFail2BanToken() error {
	// 目录挂载路径:确保目录存在(Docker 首次以 root 建目录,admin 以 1001 运行
	// 时可能尚未创建)。platform.AtomicWriteFile 只建临时文件,不建父目录。
	if err := os.MkdirAll(filepath.Dir(Fail2BanTokenPath), 0755); err != nil {
		return err
	}
	if tok := platform.GetEnv("FAIL2BAN_TOKEN"); tok != "" {
		if existing, err := os.ReadFile(Fail2BanTokenPath); err == nil && strings.TrimSpace(string(existing)) == tok {
			setFail2BanToken(tok)
			return platform.EnsurePerm(Fail2BanTokenPath, 0640)
		}
		if err := platform.AtomicWriteFile(Fail2BanTokenPath, []byte(tok), 0640); err != nil {
			return err
		}
		setFail2BanToken(tok)
		return nil
	}
	if existing, err := os.ReadFile(Fail2BanTokenPath); err == nil {
		tok := strings.TrimSpace(string(existing))
		if tok != "" {
			setFail2BanToken(tok)
			return platform.EnsurePerm(Fail2BanTokenPath, 0640)
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := hex.EncodeToString(b)
	if err := platform.AtomicWriteFile(Fail2BanTokenPath, []byte(tok), 0640); err != nil {
		return err
	}
	setFail2BanToken(tok)
	return nil
}

// EnsureProxyHtpasswd regenerates nginx's per-user XHTTP proxy htpasswd file.
func EnsureProxyHtpasswd() error {
	doc, err := ReadUsersMeta()
	if err != nil {
		return fmt.Errorf("read users: %w", err)
	}
	var entries []string
	for username, info := range doc.Users {
		if info.ProxyPassword == "" {
			continue
		}
		out, err := bcryptHtpasswdLine(username, info.ProxyPassword)
		if err != nil {
			return fmt.Errorf("bcrypt for %s: %w", username, err)
		}
		entries = append(entries, out)
	}
	if err := os.MkdirAll(NginxAuthDir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data := []byte(strings.Join(entries, "\n") + "\n")
	if len(entries) == 0 {
		data = []byte{}
	}
	if err := platform.AtomicWriteFile(ProxyHtpasswdPath, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}
