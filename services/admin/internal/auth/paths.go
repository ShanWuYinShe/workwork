package auth

// auth 域运行时文件路径(自 paths.go 迁入)。
const (
	NginxAuthDir       = "/app/data/nginx-auth"
	HtpasswdFile       = NginxAuthDir + "/.htpasswd"
	legacyHtpasswdPath = "/app/legacy-nginx/.htpasswd"
	ProxyHtpasswdPath  = NginxAuthDir + "/.htpasswd-proxy"
	InternalTokenPath  = "/app/data/.internal-token"
	Fail2BanTokenPath  = "/app/data/fail2ban/token"
)
