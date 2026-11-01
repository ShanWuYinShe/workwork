package nginxcfg

import (
	"admin/internal/runtime"
	"os"
	"strings"
	"testing"
)

const testTemplate = "server {\n    listen __ADMIN_PORT__;\n    map $http_internal_token $tok { default 0; \"~^__INTERNAL_TOKEN_REGEX__$\" 1; }\n    __XHTTP_PROXY_AUTH__\n}\n"

// testToken 覆盖字母/数字/._-，与实际可用的 token 字符集一致。
const testToken = "test-token_1234.abc"

// withNginxPaths 构造隔离的 runtime.Layout：模板放 ConfigDir，输出走 DataDir。
// 不再改写 proxy 包级路径变量。
func withNginxPaths(t *testing.T, tmpl string) (runtime.Layout, string) {
	t.Helper()
	layout := runtime.NewLayout(t.TempDir(), t.TempDir())
	if err := os.WriteFile(layout.NginxConfigTemplatePath(), []byte(tmpl), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return layout, layout.NginxConfigPath()
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
	layout, outPath := withNginxPaths(t, testTemplate)
	t.Setenv("ADMIN_PORT", "9443")
	if err := EnsureNginxConfig(testToken, layout); err != nil {
		t.Fatalf("EnsureNginxConfig: %v", err)
	}
	got := readFile(t, outPath)
	if !strings.Contains(got, "listen 9443;") {
		t.Errorf("ADMIN_PORT 未替换:\n%s", got)
	}
	if strings.Contains(got, "__ADMIN_PORT__") || strings.Contains(got, "__XHTTP_PROXY_AUTH__") ||
		strings.Contains(got, "__INTERNAL_TOKEN") {
		t.Errorf("占位符残留:\n%s", got)
	}
	// token 必须以真实值渲染进 map：nginx 只匹配值，不再"有头就豁免"。
	if !strings.Contains(got, `"~^test-token_1234\.abc$"`) {
		t.Errorf("internal token 未按真实值（regex 转义）渲染:\n%s", got)
	}
	if !strings.Contains(got, `auth_basic "XHTTP Proxy";`) ||
		!strings.Contains(got, "/etc/nginx/htpasswd/.htpasswd-proxy;") {
		t.Errorf("代理认证段不完整:\n%s", got)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Errorf("权限 = %o, want 0640", info.Mode().Perm())
	}
}

func TestEnsureNginxConfigUsesDefaultPort(t *testing.T) {
	layout, outPath := withNginxPaths(t, testTemplate)
	t.Setenv("ADMIN_PORT", "")
	if err := EnsureNginxConfig(testToken, layout); err != nil {
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
			layout, outPath := withNginxPaths(t, testTemplate)
			t.Setenv("ADMIN_PORT", bad)
			if err := EnsureNginxConfig(testToken, layout); err == nil {
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
	layout, outPath := withNginxPaths(t, testTemplate)
	t.Setenv("ADMIN_PORT", "8318")
	if err := EnsureNginxConfig(testToken, layout); err != nil {
		t.Fatalf("EnsureNginxConfig: %v", err)
	}
	before, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := os.Chmod(outPath, 0600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := EnsureNginxConfig(testToken, layout); err != nil {
		t.Fatalf("第二次 EnsureNginxConfig: %v", err)
	}
	after, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("内容未变化却重写了文件: mtime %v -> %v", before.ModTime(), after.ModTime())
	}
	if after.Mode().Perm() != 0640 {
		t.Errorf("权限未收敛: %o", after.Mode().Perm())
	}
}

// TestEnsureNginxConfigRejectsUnsafeToken: token 会写进 nginx map 正则，
// 空值/引号/$/反斜杠/空白/分号必须在写盘前拒绝，避免配置注入或空匹配。
func TestEnsureNginxConfigRejectsUnsafeToken(t *testing.T) {
	for _, tok := range []string{"", "abc def", `a"b`, "a$b", `a\b`, "a\nb", "a;b", "a{b}"} {
		t.Run(tok, func(t *testing.T) {
			layout, outPath := withNginxPaths(t, testTemplate)
			if err := EnsureNginxConfig(tok, layout); err == nil {
				t.Fatalf("unsafe token %q must be rejected", tok)
			}
			if _, err := os.Stat(outPath); !os.IsNotExist(err) {
				t.Errorf("校验失败时不应落盘配置 (stat err=%v)", err)
			}
		})
	}
}

func TestEnsureNginxConfigMissingTemplate(t *testing.T) {
	layout := runtime.NewLayout(t.TempDir(), t.TempDir())
	if err := EnsureNginxConfig(testToken, layout); err == nil {
		t.Fatal("模板缺失应当报错,而不是静默生成空配置")
	}
}
