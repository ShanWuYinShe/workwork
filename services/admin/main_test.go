package main

import (
	"testing"

	"admin/internal/platform"
)

// TestBodyLimitExemptionsRegistered 锁定"大上传端点必须在 init 声明豁免"的
// 契约:music-library 上传曾因漏加名单而线上全 400(见 music 包回归测试)。
// 新增大上传端点时: 路由注册 + 此处对应声明缺一不可。
func TestBodyLimitExemptionsRegistered(t *testing.T) {
	for _, path := range []string{
		"/api/admin/transfer/upload",
		"/api/admin/media-center/app-release",
		"/api/admin/media-center/catalog/import",
		"/api/admin/music-library/upload",
		"/api/admin/music-library/upload-chunk",
	} {
		if !platform.BodyLimitExempt(path) {
			t.Errorf("large-upload endpoint %q is not exempt from the 1MB body limit", path)
		}
	}
}
