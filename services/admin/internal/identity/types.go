package identity

import (
	"errors"
	"regexp"

	"admin/internal/platform"
)

// UserInfo 是 users.json 的单条记录。ProxyPassword 是 nginx /proxy/query 的
// Basic Auth 明文口令（懒创建，仅用于生成客户端配置）。
type UserInfo struct {
	Role          string `json:"role"`
	CreatedAt     string `json:"created_at"`
	ProxyPassword string `json:"proxy_password"`
}

// UsersDoc 是 users.json 的完整结构。
type UsersDoc struct {
	Users map[string]UserInfo `json:"users"`
}

var (
	// ErrLastAdmin 表示不能移除/降级最后一个管理员。
	ErrLastAdmin = errors.New("cannot remove the last admin user")
	// ErrProtectedAdmin 表示不能降级受保护的内置管理员。
	ErrProtectedAdmin = errors.New("cannot demote the default admin account")
	// ErrProtectedAccount 表示内置管理员账号不可经通用用户接口改写/删除。
	ErrProtectedAccount = errors.New("built-in admin account cannot be modified")
	// ErrUserNotFound 表示 users.json 中不存在该用户。
	ErrUserNotFound = errors.New("user not found")
	// 输入校验哨兵：HTTP 层据此映射 400。
	ErrInvalidUsername  = errors.New("invalid username")
	ErrPasswordTooShort = errors.New("password too short")
	ErrInvalidRole      = errors.New("invalid role")
)

var validUsername = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// IsValidUsername 校验用户名（字母/数字/_/-，≤64）。
func IsValidUsername(username string) bool {
	return username != "" && validUsername.MatchString(username) && len(username) <= 64
}

// IsReservedUsername 是内部伪身份名（internal/fail2ban）。它们由 token
// 认证链路注入，不是真实用户，不能有登录密码或 proxy 密码。
func IsReservedUsername(username string) bool {
	return username == "internal" || username == "fail2ban"
}

// Policy 是用户管理策略的唯一实现：受保护账号、最后一个管理员。
// 之前 last-admin 计数在 3 个 handler 里各写一遍，任何策略变化都可能漏改。
type Policy struct {
	ProtectedUser string
}

// NewPolicy 从 AUTH_USER 读取受保护账号。
func NewPolicy() Policy {
	return Policy{ProtectedUser: platform.GetEnv("AUTH_USER")}
}

func (p Policy) IsProtected(username string) bool {
	return username != "" && username == p.ProtectedUser
}

// CheckRoleChange 在把 username 改成 newRole 之前调用（用户不存在时跳过）。
func (p Policy) CheckRoleChange(doc UsersDoc, username, newRole string) error {
	prev, ok := doc.Users[username]
	if !ok || prev.Role != "admin" || newRole == "admin" {
		return nil
	}
	if p.IsProtected(username) {
		return ErrProtectedAdmin
	}
	if countAdmins(doc) <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// CheckDelete 在删除 username 之前调用。
func (p Policy) CheckDelete(doc UsersDoc, username string) error {
	if _, ok := doc.Users[username]; !ok {
		return nil
	}
	if p.IsProtected(username) {
		return ErrProtectedAccount
	}
	if doc.Users[username].Role == "admin" && countAdmins(doc) <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func countAdmins(doc UsersDoc) int {
	n := 0
	for _, info := range doc.Users {
		if info.Role == "admin" {
			n++
		}
	}
	return n
}
