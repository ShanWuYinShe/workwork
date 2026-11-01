package xray

import (
	"errors"
	"regexp"
	"strings"
)

// Client 是代理身份台账。Name 是网页上展示的客户端用户名，也是 Xray
// 配置里的 email（同一入站内必须唯一），日志/流量统计因此能直接看出是哪个
// 客户端。Owner 是负责该客户端的面板用户（nginx Basic Auth 身份）。
type Client struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	Name  string `json:"name,omitempty"`
	// LegacyEmail 只用于读取旧台账（旧版本 email=UUID 或历史用户名）；
	// normalize 后清空，不再写盘。
	LegacyEmail string `json:"email,omitempty"`
}

var (
	// ErrDuplicateID 表示同一 UUID 的客户端已存在。
	ErrDuplicateID = errors.New("client with this UUID already exists")
	// ErrDuplicateName 表示客户端用户名（Xray email）已被占用。
	ErrDuplicateName = errors.New("client name already exists")
	// ErrInvalidName 表示客户端用户名格式不合法。
	ErrInvalidName = errors.New("invalid client name")
	// ErrNotFound 表示客户端不存在。
	ErrNotFound = errors.New("client not found")
	// ErrForbidden 表示客户端不属于调用方。
	ErrForbidden = errors.New("forbidden: not your client")
	// ErrRestart 表示配置已变更但 Xray 无法加载（调用方应回滚并报 502）。
	ErrRestart = errors.New("xray restart failed")
	// ErrInvalidClient 表示台账条目的 UUID 为空。
	ErrInvalidClient = errors.New("invalid xray client")
)

// 客户端用户名会进入 Xray email、JSON 配置与 stats 计数器键（>>> 分隔），
// 因此限制为安全字符；Xray 对 email 做小写归一，统一存小写。
var clientNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func normalizeClientName(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ValidClientName 是 API 与配置渲染共用的用户名校验。
func ValidClientName(name string) bool {
	return clientNamePattern.MatchString(normalizeClientName(name))
}
