package nginxcfg

// nginx 配置生成:读模板(proxy.NginxConfigTemplatePath),替换占位符后写
// 运行时配置(proxy.NginxConfigPath)。media/music 拆分至独立站后,模板不再
// 含媒体占位符,本包只剩与媒体无关的通用渲染(此前内嵌于 internal/media)。

import (
	"os"
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
	out := string(tmpl)
	// XHTTP proxy auth: always use per-user proxy htpasswd
	proxyAuth := "auth_basic \"XHTTP Proxy\";\n        auth_basic_user_file /etc/nginx/htpasswd/.htpasswd-proxy;"
	out = strings.ReplaceAll(out, "__XHTTP_PROXY_AUTH__", proxyAuth)
	out = replaceEnv(out, map[string]string{
		"__ADMIN_PORT__": "ADMIN_PORT",
	}, map[string]string{
		"__ADMIN_PORT__": "8318",
	})
	if err := os.MkdirAll("/app/data/generated/nginx", 0755); err != nil {
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
