package proxy

// proxy 域运行时配置路径(自 paths.go 迁入)。
// 用变量而非常量:nginxcfg 的渲染测试需把模板/输出指向 t.TempDir(),
// 生产代码不做任何运行期改写。
var (
	XrayConfigTemplatePath  = "/app/configs/xray.json.tmpl"
	XrayConfigPath          = "/app/data/generated/xray/config.json"
	NginxConfigTemplatePath = "/app/configs/nginx.conf.tmpl"
	NginxConfigPath         = "/app/data/generated/nginx/default.conf"
)
