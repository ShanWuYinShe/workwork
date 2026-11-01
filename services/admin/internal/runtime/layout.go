package runtime

import "path/filepath"

// Layout 是运行时数据与配置路径的唯一来源。生产用 DefaultLayout()，
// 测试用 NewLayout(t.TempDir(), repoConfigsDir) 构造独立目录；不再有
// 包级路径变量或 testhooks。
type Layout struct {
	// DataDir 是 compose 挂载到 /app/data 的持久数据根。
	DataDir string
	// ConfigDir 是镜像内的模板目录（/app/configs）；测试可指向仓库 configs。
	ConfigDir string
}

// DefaultLayout 返回容器内的默认路径。
func DefaultLayout() Layout {
	return NewLayout("/app/data", "/app/configs")
}

// NewLayout 基于数据根与模板根构造路径。
func NewLayout(dataDir, configDir string) Layout {
	return Layout{DataDir: dataDir, ConfigDir: configDir}
}

func (l Layout) GeneratedDir() string     { return filepath.Join(l.DataDir, "generated") }
func (l Layout) XrayGeneratedDir() string { return filepath.Join(l.GeneratedDir(), "xray") }
func (l Layout) NginxGeneratedDir() string {
	return filepath.Join(l.GeneratedDir(), "nginx")
}
func (l Layout) NginxAuthDir() string      { return filepath.Join(l.DataDir, "nginx-auth") }
func (l Layout) UsersMetaPath() string     { return filepath.Join(l.DataDir, "users.json") }
func (l Layout) HtpasswdPath() string      { return filepath.Join(l.NginxAuthDir(), ".htpasswd") }
func (l Layout) ProxyHtpasswdPath() string { return filepath.Join(l.NginxAuthDir(), ".htpasswd-proxy") }
func (l Layout) InternalTokenPath() string { return filepath.Join(l.DataDir, ".internal-token") }
func (l Layout) Fail2BanTokenPath() string { return filepath.Join(l.DataDir, "fail2ban", "token") }
func (l Layout) XrayClientsPath() string   { return filepath.Join(l.DataDir, "xray-clients.json") }
func (l Layout) XrayConfigPath() string    { return filepath.Join(l.XrayGeneratedDir(), "config.json") }
func (l Layout) XrayConfigTemplatePath() string {
	return filepath.Join(l.ConfigDir, "xray.json.tmpl")
}
func (l Layout) NginxConfigPath() string { return filepath.Join(l.NginxGeneratedDir(), "default.conf") }
func (l Layout) NginxConfigTemplatePath() string {
	return filepath.Join(l.ConfigDir, "nginx.conf.tmpl")
}
func (l Layout) BlockedIPsPath() string { return filepath.Join(l.DataDir, "blocked-ips.json") }
func (l Layout) BlockedIPsConfPath() string {
	return filepath.Join(l.NginxGeneratedDir(), "blocked-ips.conf")
}
func (l Layout) TrafficStatePath() string { return filepath.Join(l.DataDir, "traffic-state.json") }
func (l Layout) IPv6StatePath() string    { return filepath.Join(l.DataDir, "rotating-ips.json") }
func (l Layout) TransferDir() string      { return filepath.Join(l.DataDir, "transfer") }
func (l Layout) TransferManifestPath() string {
	return filepath.Join(l.TransferDir(), "manifest.json")
}
func (l Layout) ClaudeDownloadsDir() string { return filepath.Join(l.DataDir, "claude-code") }
func (l Layout) ClaudeManifestPath() string {
	return filepath.Join(l.ClaudeDownloadsDir(), "downloads.json")
}
func (l Layout) XrayDownloadsDir() string { return filepath.Join(l.DataDir, "xray") }
func (l Layout) XrayManifestPath() string {
	return filepath.Join(l.XrayDownloadsDir(), "downloads.json")
}
