package nginxcfg

// nginx 配置生成:读模板(proxy.NginxConfigTemplatePath),替换占位符后写
// 运行时配置(proxy.NginxConfigPath)。media/music 拆分至独立站后,模板不再
// 含媒体占位符,本包只剩与媒体无关的通用渲染(此前内嵌于 internal/media)。

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"admin/internal/platform"
	"admin/internal/proxy"
)

// EnsureNginxConfig 渲染并（仅在内容变化时）写 nginx 配置。
func EnsureNginxConfig() error {
	tmpl, err := os.ReadFile(proxy.NginxConfigTemplatePath)
	if err != nil {
		return err
	}
	// ADMIN_PORT 原样注入 nginx conf,含换行/非法字符可注入伪指令:启动期
	// fail-fast(与 auth.validateAuthUserEnv 同策略),不带脏配置上线。
	if p := platform.GetEnv("ADMIN_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("ADMIN_PORT %q is invalid: must be an integer in 1-65535", p)
		}
	}
	out := string(tmpl)
	// XHTTP proxy auth: always use per-user proxy htpasswd
	proxyAuth := "auth_basic \"XHTTP Proxy\";\n        auth_basic_user_file /etc/nginx/htpasswd/.htpasswd-proxy;"
	out = strings.ReplaceAll(out, "__XHTTP_PROXY_AUTH__", proxyAuth)
	out = replaceEnv(out, map[string]string{
		"__ADMIN_PORT__": "ADMIN_PORT",
	}, map[string]string{
		"__ADMIN_PORT__": "8318",
	})
	if err := os.MkdirAll(filepath.Dir(proxy.NginxConfigPath), 0755); err != nil {
		return err
	}
	if existing, err := os.ReadFile(proxy.NginxConfigPath); err == nil && string(existing) == out {
		return platform.EnsurePerm(proxy.NginxConfigPath, 0644)
	}
	return platform.AtomicWriteFile(proxy.NginxConfigPath, []byte(out), 0644)
}

func replaceEnv(s string, vars map[string]string, defaults ...map[string]string) string {
	def := map[string]string{}
	if len(defaults) > 0 {
		def = defaults[0]
	}
	for placeholder, key := range vars {
		v := platform.GetEnv(key)
		if v == "" {
			v = def[placeholder]
		}
		s = strings.ReplaceAll(s, placeholder, v)
	}
	return s
}
