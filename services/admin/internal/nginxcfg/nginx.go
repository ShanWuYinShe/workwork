package nginxcfg

// nginx 配置生成:读模板(proxy.NginxConfigTemplatePath),替换占位符后写
// 运行时配置(proxy.NginxConfigPath)。media/music 拆分至独立站后,模板不再
// 含媒体占位符,本包只剩与媒体无关的通用渲染(此前内嵌于 internal/media)。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"admin/internal/platform"
	"admin/internal/proxy"
)

// safeInternalToken 与 auth.EnsureInternalToken 的字符集保持一致：token
// 会被写进 nginx map 正则，必须排除引号/反斜杠/$/空白等注入字符。
var safeInternalToken = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// EnsureNginxConfig 渲染并（仅在内容变化时）写 nginx 配置。
// internalToken 用于 map 的**真实值**匹配：此前 nginx 只看
// X-Internal-Token/Authorization 头是否存在就豁免限流与 IP 封禁，任意
// 伪造头即可绕过（实测伪造头 10 次全 200，不带头第 3 次 429）。
func EnsureNginxConfig(internalToken string) error {
	if !safeInternalToken.MatchString(internalToken) {
		return fmt.Errorf("internal token is empty or contains unsafe characters for nginx config")
	}
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
	out = strings.ReplaceAll(out, "__INTERNAL_TOKEN_REGEX__", regexp.QuoteMeta(internalToken))
	out = replaceEnv(out, map[string]string{
		"__ADMIN_PORT__": "ADMIN_PORT",
	}, map[string]string{
		"__ADMIN_PORT__": "8318",
	})
	// 占位符残留 = 模板/渲染器不一致，nginx 会带着字面量上线并静默失去
	// 豁免（或更糟，把占位符当 token 匹配）。fail-fast。
	if strings.Contains(out, "__INTERNAL_TOKEN") {
		return fmt.Errorf("nginx template still contains __INTERNAL_TOKEN placeholders after rendering")
	}
	if err := os.MkdirAll(filepath.Dir(proxy.NginxConfigPath), 0755); err != nil {
		return err
	}
	if existing, err := os.ReadFile(proxy.NginxConfigPath); err == nil && string(existing) == out {
		return platform.EnsurePerm(proxy.NginxConfigPath, 0640)
	}
	// 配置现在含 internal token，收紧到 0640（nginx master 以 root 读取）。
	return platform.AtomicWriteFile(proxy.NginxConfigPath, []byte(out), 0640)
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
