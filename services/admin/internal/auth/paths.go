package auth

// auth 域运行时文件路径(自 paths.go 迁入)。
// 用 var 而非 const:外部包测试需要把凭证目录重定向到临时目录，避免
// 任何写 htpasswd 的 handler 测试都依赖容器内 /app/data 可写。
var (
	NginxAuthDir      = "/app/data/nginx-auth"
	HtpasswdFile      = NginxAuthDir + "/.htpasswd"
	ProxyHtpasswdPath = NginxAuthDir + "/.htpasswd-proxy"
)

const (
	InternalTokenPath = "/app/data/.internal-token"
	Fail2BanTokenPath = "/app/data/fail2ban/token"
)
