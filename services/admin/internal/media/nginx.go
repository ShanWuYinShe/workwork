package media

import (
	"os"
	"strings"

	"admin/internal/platform"
	"admin/internal/proxy"
)

func EnsureNginxConfig() error {
	mediaCfg, err := ReadMediaCenterConfig()
	if err != nil {
		return err
	}
	_, err = RegenNginxForMediaCenter(mediaCfg)
	return err
}

// RegenNginxForMediaCenter 渲染并（仅在内容变化时）写 nginx 配置，
// 返回是否实际写入，供保存流程判断是否需要重启 nginx。
func RegenNginxForMediaCenter(mediaCfg mediaCenterConfig) (bool, error) {
	tmpl, err := os.ReadFile(proxy.NginxConfigTemplatePath)
	if err != nil {
		return false, err
	}
	out := string(tmpl)
	out = renderNginxConfigWithMediaCenter(out, mediaCfg)
	// XHTTP proxy auth: always use per-user proxy htpasswd
	proxyAuth := "auth_basic \"XHTTP Proxy\";\n        auth_basic_user_file /etc/nginx/htpasswd/.htpasswd-proxy;"
	out = strings.ReplaceAll(out, "__XHTTP_PROXY_AUTH__", proxyAuth)
	out = replaceEnv(out, map[string]string{
		"__NGINX_IPV6__": "NGINX_IPV6",
		"__ADMIN_PORT__": "ADMIN_PORT",
	}, map[string]string{
		"__ADMIN_PORT__": "8318",
	})
	if err := os.MkdirAll("/app/data/generated/nginx", 0755); err != nil {
		return false, err
	}
	if existing, err := os.ReadFile(proxy.NginxConfigPath); err == nil && string(existing) == out {
		return false, platform.EnsurePerm(proxy.NginxConfigPath, 0644)
	}
	if err := platform.AtomicWriteFile(proxy.NginxConfigPath, []byte(out), 0644); err != nil {
		return false, err
	}
	return true, nil
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
