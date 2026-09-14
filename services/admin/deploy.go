package main

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/downloads"
	"admin/internal/media"
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
	if err := media.EnsureMediaCenterConfig(); err != nil {
		return fmt.Errorf("media center config init: %w", err)
	}
	if err := accounts.GenXrayConfig(); err != nil {
		return fmt.Errorf("xray config: %w", err)
	}
	if err := media.GenMediaCenterCategories(); err != nil {
		return fmt.Errorf("media center categories: %w", err)
	}
	if err := media.EnsureNginxConfig(); err != nil {
		return fmt.Errorf("nginx config: %w", err)
	}
	if err := auth.EnsureHtpasswd(); err != nil {
		return fmt.Errorf("htpasswd: %w", err)
	}
	if err := auth.SeedAdminUser(); err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	if err := auth.EnsureProxyHtpasswd(); err != nil {
		return fmt.Errorf("xhttp htpasswd: %w", err)
	}
	if err := auth.EnsureInternalToken(); err != nil {
		return fmt.Errorf("internal token: %w", err)
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
		media.CategoriesPath,
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
		"/app/data/generated/media_center",
		"/app/data/media-center",
		auth.NginxAuthDir,
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}
