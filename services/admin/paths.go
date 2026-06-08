package main

const (
	xrayConfigTemplatePath = "/app/configs/xray.json.tmpl"
	xrayConfigPath         = "/app/data/generated/xray/config.json"
	legacyXrayConfigPath   = "/app/legacy-xray/config.json"

	nginxConfigTemplatePath = "/app/configs/nginx.conf.tmpl"
	nginxConfigPath         = "/app/data/generated/nginx/default.conf"

	nginxAuthDir       = "/app/data/nginx-auth"
	htpasswdFile       = nginxAuthDir + "/.htpasswd"
	legacyHtpasswdPath = "/app/legacy-nginx/.htpasswd"
	proxyHtpasswdPath  = nginxAuthDir + "/.htpasswd-proxy"
)
