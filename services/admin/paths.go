package main

import "regexp"

var variantRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

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
	// fail2ban token 放专用子目录:docker-compose 以目录挂载给 nginx 容器
	// (单文件 bind mount 会把缺失 source 自动创建为目录导致部署死锁,且
	// 绑定 inode 后 atomicWriteFile 的 rename 轮换在容器内永远不可见)。
	fail2banTokenPath = "/app/data/fail2ban/token"

	mediaCenterConfigPath              = "/app/data/media-center/config.json"
	mediaCenterCategoriesPath          = "/app/data/generated/media_center/contentCategories.json"
	mediaCenterCredentialsPath         = "/app/data/generated/media_center/providerCredentials.json"
	mediaCenterAppUpdateManifestPrefix = "/app/data/generated/media_center/app-update"
	mediaCenterAppUpdateAPKPrefix      = "/app/data/generated/media_center/media-center-app"
	// 发布历史（每次上传都追加一条记录），与 app-update*.json 同目录、同变体后缀。
	mediaCenterAppUpdateHistoryPrefix = "/app/data/generated/media_center/app-history"
	mediaCenterDefaultCategoriesPath  = "/app/configs/media-center-content-categories.json"
)

func sanitizeVariant(v string) string {
	if v == "" || v == "normal" {
		return ""
	}
	if !variantRe.MatchString(v) {
		return ""
	}
	return v
}

func mediaCenterAppUpdateManifestPath(variant string) string {
	v := sanitizeVariant(variant)
	if v == "" {
		return mediaCenterAppUpdateManifestPrefix + ".json"
	}
	return mediaCenterAppUpdateManifestPrefix + "-" + v + ".json"
}

func mediaCenterAppUpdateAPKPath(variant string) string {
	v := sanitizeVariant(variant)
	if v == "" {
		return mediaCenterAppUpdateAPKPrefix + ".apk"
	}
	return mediaCenterAppUpdateAPKPrefix + "-" + v + ".apk"
}

func mediaCenterAppUpdateHistoryPath(variant string) string {
	v := sanitizeVariant(variant)
	if v == "" {
		return mediaCenterAppUpdateHistoryPrefix + ".json"
	}
	return mediaCenterAppUpdateHistoryPrefix + "-" + v + ".json"
}
