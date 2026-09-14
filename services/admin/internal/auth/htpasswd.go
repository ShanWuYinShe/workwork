package auth

import (
	"admin/internal/platform"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// bcryptHtpasswdLine 返回与 `htpasswd -nbB user pass` 相同格式的行：`<user>:<bcrypt-hash>`。
// 用于替代对 htpasswd 二进制的依赖。
func bcryptHtpasswdLine(user, pass string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return user + ":" + string(hash), nil
}

// verifyHtpasswdLine 验证 (user, pass) 是否匹配 htpasswd 格式文件中该用户的行。
// bcrypt 格式跨 $2a$/$2y$ 兼容。
func verifyHtpasswdLine(user, pass, line string) bool {
	prefix := user + ":"
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	hash := strings.TrimPrefix(line, prefix)
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) == nil
}

// ProxyAuthForUser returns "username:password" for the xhttp proxy Authorization header.
// Creates a per-user proxy password on first call.
func ProxyAuthForUser(username string) (string, error) {
	if username == "" || username == "internal" {
		return "", nil
	}
	pass, err := GetOrCreateProxyPassword(username)
	if err != nil {
		return "", err
	}
	if pass == "" {
		return "", nil
	}
	return username + ":" + pass, nil
}

// ProxyAuthForUserRead 与 ProxyAuthForUser 相同，但只读取已存在的密码，
// 不创建。用于 GET /api/admin/users：读端点不应有写副作用（生成密码 + 重写
// htpasswd）。用户没有 proxy 密码时返回空串，生成的 config 中 XHTTPProxyAuth
// 为空——客户端首次添加 Xray 客户端时会懒创建并重写。
func ProxyAuthForUserRead(username string) string {
	if username == "" || username == "internal" {
		return ""
	}
	if pass := proxyPasswordIfExists(username); pass != "" {
		return username + ":" + pass
	}
	return ""
}

var htpasswdMu sync.Mutex

// VerifyHtpasswdPasswordLocked 在 htpasswdMu 保护下读取并校验 (username, password)。
// ApiChangePassword 此前在校验与 HtpasswdAddUser 重写之间不持锁——并发两次改密
// 都能通过旧密码校验后互相覆盖（last-writer-wins）。校验与重写共用一把锁后，
// 该窗口消失。
func VerifyHtpasswdPasswordLocked(username, password string) bool {
	htpasswdMu.Lock()
	defer htpasswdMu.Unlock()
	data, err := os.ReadFile(HtpasswdFile)
	if err != nil {
		return false
	}
	prefix := username + ":"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) && verifyHtpasswdLine(username, password, line) {
			return true
		}
	}
	return false
}

func HtpasswdAddUser(username, password string) error {
	if !IsValidUsername(username) {
		return fmt.Errorf("invalid username")
	}
	htpasswdMu.Lock()
	defer htpasswdMu.Unlock()

	newLine, err := bcryptHtpasswdLine(username, password)
	if err != nil {
		return err
	}
	data, readErr := os.ReadFile(HtpasswdFile)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	var lines []string
	replaced := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, username+":") {
			lines = append(lines, newLine)
			replaced = true
		} else {
			lines = append(lines, line)
		}
	}
	if !replaced {
		lines = append(lines, newLine)
	}
	// 目录缺失时自愈(正常情况下由 deploy 生成;误删后不中断写流程)。
	if err := os.MkdirAll(filepath.Dir(HtpasswdFile), 0755); err != nil {
		return err
	}
	// Atomic write via temp file + rename
	content := strings.Join(lines, "\n") + "\n"
	return platform.AtomicWriteFile(HtpasswdFile, []byte(content), 0644)
}

func HtpasswdRemoveUser(username string) error {
	if !IsValidUsername(username) {
		return fmt.Errorf("invalid username")
	}
	htpasswdMu.Lock()
	defer htpasswdMu.Unlock()

	data, readErr := os.ReadFile(HtpasswdFile)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			// htpasswd 不存在说明该用户本就不在其中,删除无需写盘
			// (也避免在只读/未初始化环境产生无意义的空文件)。
			return nil
		}
		return readErr
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, username+":") {
			lines = append(lines, line)
		}
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	// 目录缺失时自愈(与 HtpasswdAddUser 一致)。
	if err := os.MkdirAll(filepath.Dir(HtpasswdFile), 0755); err != nil {
		return err
	}
	return platform.AtomicWriteFile(HtpasswdFile, []byte(content), 0644)
}

func ReadHtpasswdUsers() []string {
	data, err := os.ReadFile(HtpasswdFile)
	if err != nil {
		return nil
	}
	var users []string
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, ":"); idx > 0 {
			users = append(users, line[:idx])
		}
	}
	return users
}

// IsValidUsername 校验用户名(小写字母/数字/_/-,≤64 字符),自 accounts 迁入共享。
var validUsername = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func IsValidUsername(username string) bool {
	return username != "" && validUsername.MatchString(username) && len(username) <= 64
}

// isReservedUsername 内部伪身份名：X-Internal-Token / X-Fail2Ban-Token 请求在
// 审计与上下文中以此名字出现（middleware RequireRole）。创建同名真实用户会
// 与其冲突——该用户永远拿不到 XHTTP proxy 密码（ProxyAuthForUser 硬编码排除），
// 且审计日志无法区分内部请求与真人操作。
func IsReservedUsername(username string) bool {
	return username == "internal" || username == "fail2ban"
}
