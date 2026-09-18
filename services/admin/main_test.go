package main

import (
	"reflect"
	"sort"
	"testing"

	"admin/internal/platform"
)

// TestBodyLimitExemptionsRegistered 锁定"大上传端点必须在 init 声明豁免"的
// 契约:music-library 上传曾因漏加名单而线上全 400(见 music 包回归测试)。
// 新增大上传端点时: 路由注册 + 此处对应声明缺一不可。
func TestBodyLimitExemptionsRegistered(t *testing.T) {
	expected := []string{
		"/api/admin/transfer/upload",
		"/api/admin/media-center/app-release",
		"/api/admin/media-center/catalog/import",
		"/api/admin/music-library/upload",
		"/api/admin/music-library/upload-chunk",
	}
	for _, path := range expected {
		if !platform.BodyLimitExempt(path) {
			t.Errorf("large-upload endpoint %q is not exempt from the 1MB body limit", path)
		}
	}
	// 双向校验:豁免集合必须恰好等于清单。单向断言发现不了"端点已删、
	// init 里的豁免声明残留"(残留豁免会让已消失的端点绕过 1MB 上限)。
	// 两侧都排序后按集合比较。
	got := platform.BodyLimitExemptions()
	wantSorted := append([]string(nil), expected...)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(got, wantSorted) {
		t.Errorf("exemption set drifted from the declared contract:\n got:  %v\n want: %v", got, wantSorted)
	}
}
