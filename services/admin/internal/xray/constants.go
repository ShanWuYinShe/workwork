package xray

// 跨 Go 代码与 xray.json.tmpl 共享的契约值。模板改动必须同步这里，
// config_test 会断言模板产物包含同一 tag / 监听地址。
const (
	InboundTagXHTTP = "xhttp-in"
	StatsAPIListen  = "127.0.0.1:10085"
)
