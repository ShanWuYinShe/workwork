package identity

import (
	"admin/internal/platform"
	"admin/internal/runtime"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// MinPasswordLen 是面板登录密码的最小长度。放在 identity 域，HTTP 层只做
// 展示/校验转发，避免后端与 UI 各写一个魔数。
const MinPasswordLen = 8

// Store 是 users.json + .htpasswd + .htpasswd-proxy 的唯一 owner。
// 所有写入经同一把 mu 串行化；跨文件操作在失败时回滚 users.json，避免
// "能登录不能代理 / 能代理不能登录" 的半状态。
type Store struct {
	mu       sync.Mutex
	layout   runtime.Layout
	users    *runtime.JSONFile[UsersDoc]
	policy   Policy
	notifier runtime.Notifier
	clock    runtime.Clock
}

func NewStore(layout runtime.Layout, notifier runtime.Notifier) *Store {
	return NewStoreWithPolicy(layout, notifier, NewPolicy())
}

func NewStoreWithPolicy(layout runtime.Layout, notifier runtime.Notifier, policy Policy) *Store {
	return &Store{
		layout:   layout,
		users:    runtime.NewJSONFile[UsersDoc](layout.UsersMetaPath(), 0600, notifier),
		policy:   policy,
		notifier: notifier,
		clock:    runtime.RealClock{},
	}
}

// WithClock 供测试注入固定时间；生产用 RealClock。
func (s *Store) WithClock(c runtime.Clock) *Store {
	s.clock = c
	return s
}

func (s *Store) loadLocked() (UsersDoc, error) {
	doc, err := s.users.Load()
	if err != nil {
		return UsersDoc{}, err
	}
	if doc.Users == nil {
		doc.Users = map[string]UserInfo{}
	}
	return doc, nil
}

func (s *Store) saveLocked(doc UsersDoc) error {
	return s.users.Save(doc)
}

func cloneDoc(doc UsersDoc) UsersDoc {
	out := UsersDoc{Users: make(map[string]UserInfo, len(doc.Users))}
	for k, v := range doc.Users {
		out.Users[k] = v
	}
	return out
}

// ReadAll 返回 users.json 的快照（map 已复制，调用方可安全遍历）。
func (s *Store) ReadAll() (UsersDoc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return UsersDoc{}, err
	}
	return cloneDoc(doc), nil
}

// Lookup 返回用户记录；不存在返回 (nil, nil)。
func (s *Store) Lookup(username string) (*UserInfo, error) {
	doc, err := s.ReadAll()
	if err != nil {
		return nil, err
	}
	info, ok := doc.Users[username]
	if !ok {
		return nil, nil
	}
	return &info, nil
}

func (s *Store) Exists(username string) (bool, error) {
	info, err := s.Lookup(username)
	return info != nil, err
}

func (s *Store) IsProtected(username string) bool {
	return s.policy.IsProtected(username)
}

// Passwords 返回 .htpasswd 中的登录用户名列表。
func (s *Store) Passwords() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readHtpasswdUsers(s.layout.HtpasswdPath())
}

// AdminView 在单次持锁内返回 users.json 与 htpasswd 用户列表，供管理页
// 组装"用户 + 角色"；分开读会在并发更新时出现角色/列表不一致。
func (s *Store) AdminView() (UsersDoc, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return UsersDoc{}, nil, err
	}
	users, err := readHtpasswdUsers(s.layout.HtpasswdPath())
	if err != nil {
		return UsersDoc{}, nil, err
	}
	return cloneDoc(doc), users, nil
}

// UpsertResult 是 UpsertUser 的结果，供审计日志使用。
type UpsertResult struct {
	Role    string
	Created bool
}

// UpsertUser 创建或更新用户：users.json 与 .htpasswd 一次事务完成。
// role 为空时编辑既有用户保留原角色、新建默认 viewer。
func (s *Store) UpsertUser(username, password, role string) (UpsertResult, error) {
	if !IsValidUsername(username) || IsReservedUsername(username) {
		return UpsertResult{}, ErrInvalidUsername
	}
	if len(password) < MinPasswordLen {
		return UpsertResult{}, ErrPasswordTooShort
	}
	if role != "" && role != "admin" && role != "viewer" {
		return UpsertResult{}, ErrInvalidRole
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked()
	if err != nil {
		return UpsertResult{}, err
	}
	prev, exists := doc.Users[username]
	if exists && s.policy.IsProtected(username) {
		return UpsertResult{}, ErrProtectedAccount
	}
	if role == "" {
		if exists && prev.Role != "" {
			role = prev.Role
		} else {
			role = "viewer"
		}
	}
	if err := s.policy.CheckRoleChange(doc, username, role); err != nil {
		return UpsertResult{}, err
	}

	newInfo := UserInfo{
		Role:      role,
		CreatedAt: s.clock.Now().UTC().Format(time.RFC3339),
	}
	if exists {
		newInfo.ProxyPassword = prev.ProxyPassword
		if prev.CreatedAt != "" {
			newInfo.CreatedAt = prev.CreatedAt
		}
	}
	before := cloneDoc(doc)
	doc.Users[username] = newInfo
	if err := s.saveLocked(doc); err != nil {
		return UpsertResult{}, err
	}
	if err := addHtpasswdLocked(s.layout.HtpasswdPath(), username, password); err != nil {
		if rbErr := s.saveLocked(before); rbErr != nil {
			log.Printf("ERROR: rollback users.json after htpasswd failure for %q: %v", username, rbErr)
		}
		return UpsertResult{}, err
	}
	return UpsertResult{Role: role, Created: !exists}, nil
}

// DeleteUser 删除用户并清理登录/代理凭证。可重入：users.json 已无该用户时
// 仍清理 htpasswd/proxy，供"删除失败后重试"补齐。
func (s *Store) DeleteUser(username string) (bool, error) {
	if !IsValidUsername(username) {
		return false, ErrInvalidUsername
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	deleted := false
	if _, exists := doc.Users[username]; exists {
		if err := s.policy.CheckDelete(doc, username); err != nil {
			return false, err
		}
		delete(doc.Users, username)
		if err := s.saveLocked(doc); err != nil {
			return false, err
		}
		deleted = true
	}
	if err := removeHtpasswdLocked(s.layout.HtpasswdPath(), username); err != nil {
		return deleted, fmt.Errorf("remove htpasswd: %w", err)
	}
	if err := s.rebuildProxyHtpasswdLocked(doc); err != nil {
		return deleted, fmt.Errorf("rebuild proxy htpasswd: %w", err)
	}
	return deleted, nil
}

func (s *Store) SetRole(username, role string) error {
	if !IsValidUsername(username) {
		return ErrInvalidUsername
	}
	if role != "admin" && role != "viewer" {
		return ErrInvalidRole
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	info, ok := doc.Users[username]
	if !ok {
		return ErrUserNotFound
	}
	if err := s.policy.CheckRoleChange(doc, username, role); err != nil {
		return err
	}
	info.Role = role
	doc.Users[username] = info
	return s.saveLocked(doc)
}

// ChangePassword 校验旧密码并替换 htpasswd 行；requireOld=false 时供 admin
// 直接重置。返回 (false, nil) 表示旧密码不正确。
func (s *Store) ChangePassword(username, oldPassword, newPassword string, requireOld bool) (bool, error) {
	if !IsValidUsername(username) {
		return false, ErrInvalidUsername
	}
	if len(newPassword) < MinPasswordLen {
		return false, ErrPasswordTooShort
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	if _, ok := doc.Users[username]; !ok {
		return false, ErrUserNotFound
	}
	if requireOld && !verifyHtpasswdFile(s.layout.HtpasswdPath(), username, oldPassword) {
		return false, nil
	}
	if err := addHtpasswdLocked(s.layout.HtpasswdPath(), username, newPassword); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureProxyPassword 为已存在用户返回（必要时懒创建）proxy 密码，并重建
// .htpasswd-proxy。用户不存在时拒绝，绝不顺手创建幽灵记录。
func (s *Store) EnsureProxyPassword(username string) (string, error) {
	if IsReservedUsername(username) {
		return "", ErrUserNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked()
	if err != nil {
		return "", err
	}
	info, ok := doc.Users[username]
	if !ok {
		return "", ErrUserNotFound
	}
	if info.ProxyPassword != "" {
		return info.ProxyPassword, nil
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	info.ProxyPassword = hex.EncodeToString(b)
	if info.CreatedAt == "" {
		info.CreatedAt = s.clock.Now().UTC().Format(time.RFC3339)
	}
	doc.Users[username] = info
	if err := s.saveLocked(doc); err != nil {
		return "", err
	}
	if err := s.rebuildProxyHtpasswdLocked(doc); err != nil {
		return "", err
	}
	return info.ProxyPassword, nil
}

// ProxyAuthForUserRead 只读取已存在的 proxy 密码，绝不创建（读端点无写副作用）。
func (s *Store) ProxyAuthForUserRead(username string) string {
	if username == "" || IsReservedUsername(username) {
		return ""
	}
	info, err := s.Lookup(username)
	if err != nil {
		log.Printf("WARNING: read proxy password for %q: %v", username, err)
		return ""
	}
	if info == nil || info.ProxyPassword == "" {
		return ""
	}
	return username + ":" + info.ProxyPassword
}

// RebuildProxyHtpasswd 从 users.json 重建 nginx /proxy/query 的凭证文件。
func (s *Store) RebuildProxyHtpasswd() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	return s.rebuildProxyHtpasswdLocked(doc)
}

func (s *Store) rebuildProxyHtpasswdLocked(doc UsersDoc) error {
	var entries []string
	for username, info := range doc.Users {
		if info.ProxyPassword == "" || IsReservedUsername(username) {
			continue
		}
		out, err := bcryptHtpasswdLine(username, info.ProxyPassword)
		if err != nil {
			return fmt.Errorf("bcrypt for %s: %w", username, err)
		}
		entries = append(entries, out)
	}
	if err := os.MkdirAll(s.layout.NginxAuthDir(), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data := []byte(strings.Join(entries, "\n") + "\n")
	if len(entries) == 0 {
		data = []byte{}
	}
	return writeHtpasswdFile(s.layout.ProxyHtpasswdPath(), data)
}

// EnsureHtpasswd 启动引导：校验/修复 .htpasswd；文件损坏时隔离并按
// AUTH_USER/AUTH_PASS 重建。
func (s *Store) EnsureHtpasswd() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.layout.HtpasswdPath()
	if err := os.MkdirAll(s.layout.NginxAuthDir(), 0755); err != nil {
		return err
	}
	if err := validateAuthUserEnv(); err != nil {
		return err
	}
	if data, err := os.ReadFile(path); err == nil {
		if htpasswdHasValidEntry(data) {
			ensureHtpasswdPerm(path)
			return nil
		}
		log.Printf("ERROR: htpasswd %s has no valid user:hash entry, quarantining and rebuilding from AUTH_USER/AUTH_PASS", path)
		if qerr := os.Rename(path, path+".corrupt"); qerr != nil {
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
	return writeHtpasswdFile(path, []byte(out))
}

func validateAuthUserEnv() error {
	user := platform.GetEnv("AUTH_USER")
	if user == "" {
		return nil
	}
	if !IsValidUsername(user) {
		return fmt.Errorf("AUTH_USER %q is invalid: must match [a-zA-Z0-9_-]{1,64}", user)
	}
	return nil
}

// SeedAdminUser 确保 AUTH_USER 在 users.json 中存在且为 admin（不改密码）。
func (s *Store) SeedAdminUser() error {
	adminUser := platform.GetEnv("AUTH_USER")
	if adminUser == "" {
		log.Printf("WARNING: AUTH_USER not set; the protected-admin guard (role/demote/delete protection) is INACTIVE")
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	if _, exists := doc.Users[adminUser]; exists {
		return nil
	}
	doc.Users[adminUser] = UserInfo{Role: "admin", CreatedAt: s.clock.Now().UTC().Format(time.RFC3339)}
	return s.saveLocked(doc)
}

// Reconcile 对账 users.json 与 .htpasswd：
//   - htpasswd 有、users.json 无：幽灵凭证，删除该行；
//   - users.json 有、htpasswd 无：无法登录，记 ERROR + webhook（保留伪用户）；
//   - 保留名（internal/fail2ban）不参与缺失告警。
func (s *Store) Reconcile() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	htUsers, err := readHtpasswdUsers(s.layout.HtpasswdPath())
	if err != nil && !os.IsNotExist(err) {
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
		s.notify("auth.orphan", "htpasswd 存在无 users.json 条目的幽灵凭证，已删除", map[string]string{"username": u})
		if err := removeHtpasswdLocked(s.layout.HtpasswdPath(), u); err != nil {
			return fmt.Errorf("remove orphan htpasswd user %q: %w", u, err)
		}
	}
	for u := range doc.Users {
		if inHt[u] || IsReservedUsername(u) {
			continue
		}
		log.Printf("ERROR: users.json user %q has no htpasswd entry; login is impossible until the password is reset", u)
		s.notify("auth.drift", "users.json 用户缺少 htpasswd 条目，无法登录", map[string]string{"username": u})
	}
	return nil
}

func (s *Store) notify(event, summary string, details map[string]string) {
	if s.notifier != nil {
		s.notifier.Notify(event, summary, details)
	}
}
