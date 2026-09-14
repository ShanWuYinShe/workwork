package proxy

// proxy 域运行时配置路径(自 paths.go 迁入)。
const (
	XrayConfigTemplatePath  = "/app/configs/xray.json.tmpl"
	XrayConfigPath          = "/app/data/generated/xray/config.json"
	NginxConfigTemplatePath = "/app/configs/nginx.conf.tmpl"
	NginxConfigPath         = "/app/data/generated/nginx/default.conf"
)
