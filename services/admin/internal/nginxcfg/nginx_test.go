package nginxcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"admin/internal/proxy"
)

const testTemplate = "server {\n    listen __ADMIN_PORT__;\n    __XHTTP_PROXY_AUTH__\n}\n"

// withNginxPaths 把渲染的模板/输出路径指向 t.TempDir() 并写入模板,返回输出路径。
// 生产路径是包级变量(见 proxy/paths.go),仅测试会改写,结束即还原。
func withNginxPaths(t *testing.T, tmpl string) string {
	t.Helper()
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "nginx.conf.tmpl")
	outPath := filepath.Join(dir, "generated", "nginx", "default.conf")
	if err := os.WriteFile(tmplPath, []byte(tmpl), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	oldTmpl, oldOut := proxy.NginxConfigTemplatePath, proxy.NginxConfigPath
	proxy.NginxConfigTemplatePath, proxy.NginxConfigPath = tmplPath, outPath
	t.Cleanup(func() {
		proxy.NginxConfigTemplatePath, proxy.NginxConfigPath = oldTmpl, oldOut
	})
	return outPath
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestEnsureNginxConfigRendersPlaceholders(t *testing.T) {
	outPath := withNginxPaths(t, testTemplate)
	t.Setenv("ADMIN_PORT", "9443")
	if err := EnsureNginxConfig(); err != nil {
		t.Fatalf("EnsureNginxConfig: %v", err)
	}
	got := readFile(t, outPath)
	if !strings.Contains(got, "listen 9443;") {
		t.Errorf("ADMIN_PORT 未替换:\n%s", got)
	}
	if strings.Contains(got, "__ADMIN_PORT__") || strings.Contains(got, "__XHTTP_PROXY_AUTH__") {
		t.Errorf("占位符残留:\n%s", got)
	}
	if !strings.Contains(got, `auth_basic "XHTTP Proxy";`) ||
		!strings.Contains(got, "/etc/nginx/htpasswd/.htpasswd-proxy;") {
		t.Errorf("代理认证段不完整:\n%s", got)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("权限 = %o, want 0644", info.Mode().Perm())
	}
}

func TestEnsureNginxConfigUsesDefaultPort(t *testing.T) {
	outPath := withNginxPaths(t, testTemplate)
	t.Setenv("ADMIN_PORT", "")
	if err := EnsureNginxConfig(); err != nil {
		t.Fatalf("EnsureNginxConfig: %v", err)
	}
	if got := readFile(t, outPath); !strings.Contains(got, "listen 8318;") {
		t.Errorf("ADMIN_PORT 缺省应为 8318:\n%s", got)
	}
}

// TestEnsureNginxConfigRejectsInjectedPort 是安全回归护栏:ADMIN_PORT 的取值
// 原样注入 nginx 配置,非法值必须在写盘之前失败——否则可注入伪指令(任意
// nginx 配置执行面)。同时断言失败路径不留半成品配置。
func TestEnsureNginxConfigRejectsInjectedPort(t *testing.T) {
	badPorts := []string{
		"8080;\n    return 200",
		"80;worker_processes 8",
		"80 extra",
		"abc",
		"0",
		"65536",
		"-1",
		" 8080",
	}
	for _, bad := range badPorts {
		t.Run(bad, func(t *testing.T) {
			outPath := withNginxPaths(t, testTemplate)
			t.Setenv("ADMIN_PORT", bad)
			if err := EnsureNginxConfig(); err == nil {
				t.Fatalf("非法 ADMIN_PORT %q 应当报错", bad)
			}
			if _, err := os.Stat(outPath); !os.IsNotExist(err) {
				t.Errorf("校验失败时不应落盘配置 (stat err=%v)", err)
			}
		})
	}
}

// TestEnsureNginxConfigSkipsUnchangedWrite: 内容一致时不重写(避免 nginx 侧
// inode 抖动),但仍要收敛权限(旧版本可能落过更宽的权限)。
func TestEnsureNginxConfigSkipsUnchangedWrite(t *testing.T) {
	outPath := withNginxPaths(t, testTemplate)
	t.Setenv("ADMIN_PORT", "8318")
	if err := EnsureNginxConfig(); err != nil {
		t.Fatalf("EnsureNginxConfig: %v", err)
	}
	before, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := os.Chmod(outPath, 0600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := EnsureNginxConfig(); err != nil {
		t.Fatalf("第二次 EnsureNginxConfig: %v", err)
	}
	after, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("内容未变化却重写了文件: mtime %v -> %v", before.ModTime(), after.ModTime())
	}
	if after.Mode().Perm() != 0644 {
		t.Errorf("权限未收敛: %o", after.Mode().Perm())
	}
}

func TestEnsureNginxConfigMissingTemplate(t *testing.T) {
	dir := t.TempDir()
	oldTmpl, oldOut := proxy.NginxConfigTemplatePath, proxy.NginxConfigPath
	proxy.NginxConfigTemplatePath = filepath.Join(dir, "missing.tmpl")
	proxy.NginxConfigPath = filepath.Join(dir, "out.conf")
	t.Cleanup(func() {
		proxy.NginxConfigTemplatePath, proxy.NginxConfigPath = oldTmpl, oldOut
	})
	if err := EnsureNginxConfig(); err == nil {
		t.Fatal("模板缺失应当报错,而不是静默生成空配置")
	}
}
