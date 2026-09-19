package media

import "regexp"

var variantRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// media 域运行时文件路径(自主包 paths.go 迁入)。ConfigPath/CategoriesPath
// 为 var 供测试替换(与 musicLibraryDir/xrayClientsPath 同一模式)。
var (
	ConfigPath      = "/app/data/media-center/config.json"
	CategoriesPath  = "/app/data/generated/media_center/contentCategories.json"
	CredentialsPath = "/app/data/generated/media_center/providerCredentials.json"
)

const (
	AppUpdateManifestPrefix = "/app/data/generated/media_center/app-update"
	AppUpdateAPKPrefix      = "/app/data/generated/media_center/media-center-app"
	// 发布历史（每次上传都追加一条记录），与 app-update*.json 同目录、同变体后缀。
	AppUpdateHistoryPrefix = "/app/data/generated/media_center/app-history"
	DefaultCategoriesPath  = "/app/configs/media-center-content-categories.json"
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

func appUpdateManifestPath(variant string) string {
	v := sanitizeVariant(variant)
	if v == "" {
		return AppUpdateManifestPrefix + ".json"
	}
	return AppUpdateManifestPrefix + "-" + v + ".json"
}

func appUpdateAPKPath(variant string) string {
	v := sanitizeVariant(variant)
	if v == "" {
		return AppUpdateAPKPrefix + ".apk"
	}
	return AppUpdateAPKPrefix + "-" + v + ".apk"
}

func appUpdateHistoryPath(variant string) string {
	v := sanitizeVariant(variant)
	if v == "" {
		return AppUpdateHistoryPrefix + ".json"
	}
	return AppUpdateHistoryPrefix + "-" + v + ".json"
}
