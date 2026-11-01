package xray

import "errors"

// Client 是代理身份台账。Email 恒等于 ID（Xray 要求同一入站内 email 唯一），
// Owner 是负责该客户端的面板用户（nginx Basic Auth 身份）。
type Client struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	Email string `json:"email"`
}

var (
	// ErrDuplicateID 表示同一 UUID 的客户端已存在。
	ErrDuplicateID = errors.New("client with this UUID already exists")
	// ErrNotFound 表示客户端不存在。
	ErrNotFound = errors.New("client not found")
	// ErrForbidden 表示客户端不属于调用方。
	ErrForbidden = errors.New("forbidden: not your client")
	// ErrRestart 表示配置已变更但 Xray 无法加载（调用方应回滚并报 502）。
	ErrRestart = errors.New("xray restart failed")
	// ErrInvalidClient 表示台账条目的 UUID 为空。
	ErrInvalidClient = errors.New("invalid xray client")
)
