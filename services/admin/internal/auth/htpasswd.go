package auth

import (
	"admin/internal/platform"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"log"
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

// htpasswdGID 是 nginx worker 的运行组(compose nginx group_add/uid 101)。
// htpasswd 文件 0640 owner=1001 group=101:nginx 可读,宿主其他用户不可读
// (0644 时全部用户都能拿到 bcrypt 哈希做离线爆破)。
const htpasswdGID = 101

// writeHtpasswdFile 原子写入 htpasswd 凭证文件并收紧权限(0640 + chgrp 101)。
// chgrp 失败(进程无 101 附属组,如本地未配 compose group_add)时退回 0644
// 并留 WARNING:宁可维持旧的可读性也不能让 nginx 读不了 htpasswd——那会
// 整站 basic auth 全员失效。
func writeHtpasswdFile(path string, content []byte) error {
	if err := platform.AtomicWriteFile(path, content, 0640); err != nil {
		return err
	}
	if err := os.Chown(path, -1, htpasswdGID); err != nil {
		if chmodErr := os.Chmod(path, 0644); chmodErr != nil {
			log.Printf("ERROR: fallback chmod 0644 %s: %v", path, chmodErr)
		}
		log.Printf("WARNING: chgrp %d %s failed (%v); keeping 0644 — add group_add [\"%d\"] to the admin service", htpasswdGID, path, err, htpasswdGID)
		return nil
	}
	return nil
}

// ensureHtpasswdPerm 对已存在的 htpasswd 文件做幂等权限收敛(启动自愈:
// 旧版本创建的 0644 文件在升级后自动收紧)。
func ensureHtpasswdPerm(path string) {
	if err := os.Chown(path, -1, htpasswdGID); err != nil {
		log.Printf("WARNING: chgrp %d %s failed (%v); file stays readable — add group_add [\"%d\"] to the admin service", htpasswdGID, path, err, htpasswdGID)
		return
	}
	if err := platform.EnsurePerm(path, 0640); err != nil {
		log.Printf("WARNING: tighten perm %s: %v", path, err)
	}
}

// VerifyHtpasswdPasswordLocked 在 htpasswdMu 保护下读取并校验 (username, password)。
func VerifyHtpasswdPasswordLocked(username, password string) bool {
	htpasswdMu.Lock()
	defer htpasswdMu.Unlock()
	return verifyHtpasswdFile(username, password)
}

// HtpasswdChangePassword 在单次持锁内完成旧密码校验与新密码写入。此前
// verify 与重写是两次独立加锁——并发两次改密都能通过旧密码校验后互相
// 覆盖(last-writer-wins),旧注释声称"共用一把锁后窗口消失"与实现不符。
// 返回 (false, nil) 表示旧密码校验失败。
func HtpasswdChangePassword(username, oldPassword, newPassword string) (bool, error) {
	if !IsValidUsername(username) {
		return false, fmt.Errorf("invalid username")
	}
	htpasswdMu.Lock()
	defer htpasswdMu.Unlock()
	if !verifyHtpasswdFile(username, oldPassword) {
		return false, nil
	}
	if err := htpasswdAddUserLocked(username, newPassword); err != nil {
		return false, err
	}
	return true, nil
}

// verifyHtpasswdFile 供已持有 htpasswdMu 的调用方使用。
func verifyHtpasswdFile(username, password string) bool {
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
	return htpasswdAddUserLocked(username, password)
}

// htpasswdAddUserLocked 供已持有 htpasswdMu 的调用方使用。
func htpasswdAddUserLocked(username, password string) error {
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
	content := strings.Join(lines, "\n") + "\n"
	return writeHtpasswdFile(HtpasswdFile, []byte(content))
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
	// 必须走 writeHtpasswdFile(0640+chgrp 101):此前直接 0644 落盘,删除用户后
	// bcrypt 哈希对本机全部用户可读,直到下次凭证写入或重启自愈才恢复收敛。
	return writeHtpasswdFile(HtpasswdFile, []byte(content))
}

// 返回错误而非静默 nil:吞错会让 admin 用户列表静默为空(展示误导)。
func ReadHtpasswdUsers() ([]string, error) {
	data, err := os.ReadFile(HtpasswdFile)
	if err != nil {
		return nil, err
	}
	var users []string
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, ":"); idx > 0 {
			users = append(users, line[:idx])
		}
	}
	return users, nil
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
