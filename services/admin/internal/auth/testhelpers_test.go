package auth

import (
	"admin/internal/identity"
	"admin/internal/runtime"
	"testing"
)

// newTestMiddleware 构造隔离的 identity store + secrets。之前测试靠
// testhooks 改包级路径/atomic token；现在每个测试拿到自己的 temp Layout。
func newTestMiddleware(t *testing.T) *Middleware {
	t.Helper()
	layout := runtime.NewLayout(t.TempDir(), t.TempDir())
	users := identity.NewStore(layout, nil)
	secrets := identity.NewSecrets(layout)
	return NewMiddleware(users, secrets, NewSecurity(layout))
}

// useInternalToken 设置 env token 并完成 secrets 初始化。
func useInternalToken(t *testing.T) *Middleware {
	t.Helper()
	mw := newTestMiddleware(t)
	t.Setenv("INTERNAL_TOKEN", "internal-secret")
	if err := mw.secrets.EnsureInternalToken(); err != nil {
		t.Fatalf("ensure internal token: %v", err)
	}
	return mw
}
