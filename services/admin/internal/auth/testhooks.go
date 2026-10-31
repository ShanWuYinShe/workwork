// 测试钩子: 仅供其他包的测试使用,生产代码禁止调用。

package auth

import (
	"path/filepath"
	"testing"
)

// WithUsersMetaPath 供外部包测试覆盖 users.json 路径。
// 生产代码不得调用(仅 accounts 包测试使用)。
func WithUsersMetaPath(t testing.TB, path string) {
	old := usersMetaPath
	usersMetaPath = path
	t.Cleanup(func() { usersMetaPath = old })
}

// WithNginxAuthDir 供外部包测试把 .htpasswd/.htpasswd-proxy 重定向到临时
// 目录。写凭证的 handler（删用户级联清理、加客户端懒建 proxy 密码）需要
// 目录可写；生产默认路径在容器内是 /app/data/nginx-auth。
func WithNginxAuthDir(t testing.TB, dir string) {
	t.Helper()
	oldDir, oldHtpasswd, oldProxy := NginxAuthDir, HtpasswdFile, ProxyHtpasswdPath
	NginxAuthDir = dir
	HtpasswdFile = filepath.Join(dir, ".htpasswd")
	ProxyHtpasswdPath = filepath.Join(dir, ".htpasswd-proxy")
	t.Cleanup(func() {
		NginxAuthDir, HtpasswdFile, ProxyHtpasswdPath = oldDir, oldHtpasswd, oldProxy
	})
}
