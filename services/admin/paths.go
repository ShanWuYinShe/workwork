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

	internalTokenPath = "/app/data/.internal-token"
	fail2banTokenPath = "/app/data/.fail2ban-token"

	mediaCenterConfigPath            = "/app/data/media-center/config.json"
	mediaCenterCategoriesPath        = "/app/data/generated/media_center/contentCategories.json"
	mediaCenterAppUpdateManifestPath = "/app/data/generated/media_center/app-update.json"
	mediaCenterAppUpdateAPKPath      = "/app/data/generated/media_center/media-center-app.apk"
	mediaCenterDefaultCategoriesPath = "/app/configs/media-center-content-categories.json"
)
