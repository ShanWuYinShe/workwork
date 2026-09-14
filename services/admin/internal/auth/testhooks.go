// 测试钩子: 仅供其他包的测试使用,生产代码禁止调用。

package auth

import "testing"

// WithUsersMetaPath 供外部包测试覆盖 users.json 路径。
// 生产代码不得调用(仅 accounts 包测试使用)。
func WithUsersMetaPath(t testing.TB, path string) {
	old := usersMetaPath
	usersMetaPath = path
	t.Cleanup(func() { usersMetaPath = old })
}
