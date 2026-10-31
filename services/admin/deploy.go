package main

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/downloads"
	"admin/internal/nginxcfg"
	"admin/internal/proxy"
	"fmt"
	"os"
)

// generateConfigs creates runtime configs and auth files from templates.
// Called on admin startup before xray/nginx are allowed to start.
func generateConfigs() error {
	if err := ensureRuntimeDirs(); err != nil {
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
	if err := auth.EnsureInternalToken(); err != nil {
		return fmt.Errorf("internal token: %w", err)
	}
	if err := nginxcfg.EnsureNginxConfig(auth.InternalToken()); err != nil {
		return fmt.Errorf("nginx config: %w", err)
	}
	// 封禁 map 配置是 nginx 配置的硬依赖(undefined variable 即 emerg),
	// 必须在 nginx 容器启动前同步落盘;后续变更仍走 2s 去抖的异步同步。
	if err := auth.WriteBlockedIPsConf(); err != nil {
		return fmt.Errorf("nginx blocked-ips conf: %w", err)
	}
	if err := auth.EnsureHtpasswd(); err != nil {
		return fmt.Errorf("htpasswd: %w", err)
	}
	if err := auth.SeedAdminUser(); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	// 对账 users.json ↔ .htpasswd：清理幽灵凭证，告警无法登录的账号。
	if err := auth.ReconcileAuthState(); err != nil {
		return fmt.Errorf("auth state reconcile: %w", err)
	}
	if err := auth.EnsureProxyHtpasswd(); err != nil {
		return fmt.Errorf("xhttp htpasswd: %w", err)
	}
	if err := auth.EnsureFail2BanToken(); err != nil {
		return fmt.Errorf("fail2ban token: %w", err)
	}
	return nil
}

func runtimeFilesReady() error {
	required := []string{
		proxy.XrayConfigPath,
		proxy.NginxConfigPath,
		auth.HtpasswdFile,
		auth.ProxyHtpasswdPath,
		auth.InternalTokenPath,
		auth.Fail2BanTokenPath,
	}
	for _, path := range required {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if path != auth.ProxyHtpasswdPath && info.Size() == 0 {
			return fmt.Errorf("%s is empty", path)
		}
	}
	return nil
}

func ensureRuntimeDirs() error {
	for _, dir := range []string{
		"/app/data/generated/xray",
		"/app/data/generated/nginx",
		auth.NginxAuthDir,
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}
