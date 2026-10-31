package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"admin/internal/platform"
)

// validInternalToken 限定 token 字符集：它会被原样写进 nginx 配置的 map
// 正则做真实值匹配。允许字母/数字/._~-，覆盖自动生成的 hex，同时排除
// 引号、反斜杠、$、空白等会破坏/注入 nginx 配置的字符。
var validInternalToken = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

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

// ReconcileAuthState 对账 users.json 与 .htpasswd 两个真相源：
//   - htpasswd 有、users.json 无：幽灵凭证。它能过 nginx basic auth，但
//     Go 侧会以"用户未初始化"403，且管理页看不到、删不掉。启动时删除该行。
//   - users.json 有、htpasswd 无：该用户无法登录（密码哈希只在 htpasswd），
//     无法自动恢复（不能拿 .env 里的旧 AUTH_PASS 覆盖已改过的密码）；
//     记 ERROR + webhook，提示管理员重置密码。
//
// 正常写流程会同时更新两者；漂移来自部分失败（写 users.json 成功、写
// htpasswd 失败）或手工改文件，启动对账把长期残留收敛掉。
func ReconcileAuthState() error {
	doc, err := ReadUsersMeta()
	if err != nil {
		return err
	}
	htUsers, err := ReadHtpasswdUsers()
	if err != nil {
		return err
	}
	inMeta := make(map[string]bool, len(doc.Users))
	for u := range doc.Users {
		inMeta[u] = true
	}
	inHt := make(map[string]bool, len(htUsers))
	for _, u := range htUsers {
		inHt[u] = true
		if inMeta[u] {
			continue
		}
		log.Printf("ERROR: htpasswd user %q has no users.json entry; removing orphan credential", u)
		platform.NotifyWebhook("auth.orphan", "htpasswd 存在无 users.json 条目的幽灵凭证，已删除", map[string]string{"username": u})
		if err := HtpasswdRemoveUser(u); err != nil {
			return fmt.Errorf("remove orphan htpasswd user %q: %w", u, err)
		}
	}
	for u := range doc.Users {
		if inHt[u] {
			continue
		}
		log.Printf("ERROR: users.json user %q has no htpasswd entry; login is impossible until the password is reset", u)
		platform.NotifyWebhook("auth.drift", "users.json 用户缺少 htpasswd 条目，无法登录", map[string]string{"username": u})
	}
	return nil
}

func EnsureInternalToken() error {
	if tok := platform.GetEnv("INTERNAL_TOKEN"); tok != "" {
		if !validInternalToken.MatchString(tok) {
			return fmt.Errorf("INTERNAL_TOKEN contains characters outside [A-Za-z0-9._~-]; it is rendered into the nginx config and must not contain quotes, $, backslashes or whitespace")
		}
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
			if !validInternalToken.MatchString(tok) {
				return fmt.Errorf("existing token %s contains characters outside [A-Za-z0-9._~-]; delete it to regenerate, or set INTERNAL_TOKEN explicitly", InternalTokenPath)
			}
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

// proxyHtpasswdMu 串行化"读 users.json → bcrypt → 写 .htpasswd-proxy"的
// 完整重建。两个用户并发首次创建 proxy 密码时，A 的旧快照可能覆盖 B 刚写入
// 的行，导致 B 拿着 users.json 里的正确密码却被 nginx 401，直到下次重建。
var proxyHtpasswdMu sync.Mutex

// EnsureProxyHtpasswd regenerates nginx's per-user XHTTP proxy htpasswd file.
func EnsureProxyHtpasswd() error {
	proxyHtpasswdMu.Lock()
	defer proxyHtpasswdMu.Unlock()

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
