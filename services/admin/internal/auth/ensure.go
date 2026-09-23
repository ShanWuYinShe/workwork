package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
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
	if err := validateAuthUserEnv(); err != nil {
		return err
	}
	if data, err := os.ReadFile(HtpasswdFile); err == nil {
		// "文件存在且超过 10 字节"不足以判定完好:截断到几十字节时启动
		// 照常通过,但所有行(含 AUTH_USER)都不是有效的 user:hash 条目,
		// 面板与 nginx basic auth 全员校验失败 = 整站锁死且无提示。
		// 校验"至少一行合法条目",零条目时隔离损坏文件并按 env 重建。
		if htpasswdHasValidEntry(data) {
			ensureHtpasswdPerm(HtpasswdFile)
			return nil
		}
		log.Printf("ERROR: htpasswd %s has no valid user:hash entry, quarantining and rebuilding from AUTH_USER/AUTH_PASS", HtpasswdFile)
		if qerr := os.Rename(HtpasswdFile, HtpasswdFile+".corrupt"); qerr != nil {
			log.Printf("ERROR: quarantine htpasswd: %v", qerr)
		}
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
	return writeHtpasswdFile(HtpasswdFile, []byte(out))
}

// validateAuthUserEnv 校验 AUTH_USER(env 直入 htpasswd 行与 users.json 键,
// 不经过 API 路径的 IsValidUsername 校验):含冒号/换行会破坏 htpasswd 行
// 结构(注入任意凭证条目),格式非法时启动即失败,不让坏配置上线。
func validateAuthUserEnv() error {
	user := platform.GetEnv("AUTH_USER")
	if user == "" {
		// 空值本身合法(不种子内置管理员);危害是 IsProtectedAdmin 守卫
		// 静默失活,在 SeedAdminUser 里告警。
		return nil
	}
	if !IsValidUsername(user) {
		return fmt.Errorf("AUTH_USER %q is invalid: must match [a-zA-Z0-9_-]{1,64}", user)
	}
	return nil
}

// htpasswdHasValidEntry 判断文件内容中是否存在至少一行 "user:hash" 形式
// 的条目(不校验 hash 算法细节——htpasswd 支持多种格式,这里只挡截断/清空)。
func htpasswdHasValidEntry(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 && idx < len(line)-1 {
			return true
		}
	}
	return false
}

func SeedAdminUser() error {
	adminUser := platform.GetEnv("AUTH_USER")
	if adminUser == "" {
		// 空值时 IsProtectedAdmin(AUTH_USER 比对)恒为 false——内置管理员
		// 保护静默失活,这不是合法配置,启动时明确告警。
		log.Printf("WARNING: AUTH_USER not set; the protected-admin guard (role/demote/delete protection) is INACTIVE")
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
	if err := writeHtpasswdFile(ProxyHtpasswdPath, data); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}
