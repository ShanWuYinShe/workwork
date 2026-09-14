package main

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/media"
	"admin/internal/proxy"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// generateConfigs creates runtime configs and auth files from templates.
// Called on admin startup before xray/nginx are allowed to start.
func generateConfigs() error {
	if err := ensureRuntimeDirs(); err != nil {
		return fmt.Errorf("runtime dirs: %w", err)
	}
	// 清理上次运行被中断下载残留的临时文件(不返回错误:清理失败只影响
	// 磁盘占用,不应阻断启动)。
	purgeStaleDownloadTemps()
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

// purgeStaleDownloadTemps 清理上次运行残留的 ".download-*" 临时文件。
//
// download{Claude,Xray}Archive 用 defer os.Remove 清理正常/失败路径,但容器在
// 下载中途被 kill 或重建(如 docker compose up -d admin)时 defer 不执行,临时
// 文件会永久留在下载目录:既占磁盘(claude/Xray 包可达数百 MB),又会被
// apiClaudeCodeServeDownload / apiXrayServeDownload 的路径校验拒之门外——
// 永远不可下载也不可删除的僵尸文件。启动是唯一可靠的清理时机(此刻不可能有
// 正在进行的下载)。
func purgeStaleDownloadTemps() {
	for _, dir := range downloadDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // 目录不存在:尚未下载过任何文件
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.Contains(name, ".download-") {
				continue
			}
			p := filepath.Join(dir, name)
			if err := os.Remove(p); err != nil {
				log.Printf("remove stale download temp %s: %v", p, err)
				continue
			}
			log.Printf("removed stale download temp: %s", p)
		}
	}
}
