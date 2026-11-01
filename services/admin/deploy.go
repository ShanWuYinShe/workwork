package main

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/downloads"
	"admin/internal/identity"
	"admin/internal/nginxcfg"
	"admin/internal/runtime"
	"fmt"
	"os"
)

// generateConfigs 创建运行时配置与认证文件，供 xray/nginx 启动前使用。
// 依赖显式传入（identity store / secrets / layout），不再由 auth 包级全局
// 变量与散落的 /app/data 路径隐式提供。
func generateConfigs(users *identity.Store, secrets *identity.Secrets, layout runtime.Layout) error {
	if err := ensureRuntimeDirs(layout); err != nil {
		return fmt.Errorf("runtime dirs: %w", err)
	}
	// 清理上次运行被中断下载残留的临时文件(不返回错误:清理失败只影响
	// 磁盘占用,不应阻断启动)。
	downloads.PurgeStaleDownloadTemps()
	if err := accounts.ValidateProxyConfigEnv(); err != nil {
		return fmt.Errorf("proxy env: %w", err)
	}
	if err := accounts.EnsureXrayClients(); err != nil {
		return fmt.Errorf("xray clients init: %w", err)
	}
	if _, err := accounts.GenXrayConfig(); err != nil {
		return fmt.Errorf("xray config: %w", err)
	}
	// token 必须先就绪：nginx 配置要把 token 的真实值渲染进 map（值匹配，
	// 而不是"头存在即豁免"）。顺序颠倒会渲染出空/过期 token。
	if err := secrets.EnsureInternalToken(); err != nil {
		return fmt.Errorf("internal token: %w", err)
	}
	if err := nginxcfg.EnsureNginxConfig(secrets.InternalToken()); err != nil {
		return fmt.Errorf("nginx config: %w", err)
	}
	// 封禁 map 配置是 nginx 配置的硬依赖(undefined variable 即 emerg),
	// 必须在 nginx 容器启动前同步落盘;后续变更仍走 2s 去抖的异步同步。
	if err := auth.WriteBlockedIPsConf(); err != nil {
		return fmt.Errorf("nginx blocked-ips conf: %w", err)
	}
	if err := users.EnsureHtpasswd(); err != nil {
		return fmt.Errorf("htpasswd: %w", err)
	}
	if err := users.SeedAdminUser(); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	// 对账 users.json ↔ .htpasswd：清理幽灵凭证，告警无法登录的账号。
	if err := users.Reconcile(); err != nil {
		return fmt.Errorf("auth state reconcile: %w", err)
	}
	if err := users.RebuildProxyHtpasswd(); err != nil {
		return fmt.Errorf("xhttp htpasswd: %w", err)
	}
	if err := secrets.EnsureFail2BanToken(); err != nil {
		return fmt.Errorf("fail2ban token: %w", err)
	}
	return nil
}

func runtimeFilesReady(layout runtime.Layout) error {
	required := []string{
		layout.XrayConfigPath(),
		layout.NginxConfigPath(),
		layout.HtpasswdPath(),
		layout.ProxyHtpasswdPath(),
		layout.InternalTokenPath(),
		layout.Fail2BanTokenPath(),
	}
	for _, path := range required {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if path != layout.ProxyHtpasswdPath() && info.Size() == 0 {
			return fmt.Errorf("%s is empty", path)
		}
	}
	return nil
}

func ensureRuntimeDirs(layout runtime.Layout) error {
	for _, dir := range []string{
		layout.XrayGeneratedDir(),
		layout.NginxGeneratedDir(),
		layout.NginxAuthDir(),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}
