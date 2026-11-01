package main

import (
	"admin/internal/identity"
	"admin/internal/runtime"
	"admin/internal/transfer"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"admin/internal/platform"
)

// TestRouteTableContract 锁定路由表的全局不变量。此前这些属性分散在
// main() 的内联 HandleFunc、init() 的 body-limit 清单和各 handler 的日志里，
// 只能靠源码正则扫描；现在路由表就是契约本身。
func TestRouteTableContract(t *testing.T) {
	routes := apiRoutes(nil, identity.NewStore(runtime.NewLayout(t.TempDir(), t.TempDir()), nil), nil, transfer.NewStore(runtime.NewLayout(t.TempDir(), t.TempDir())), runtime.NewLayout(t.TempDir(), t.TempDir()))
	seen := map[string]bool{}
	var exempt []string

	for _, rt := range routes {
		key := rt.PatternWithMethod()
		if seen[key] {
			t.Errorf("duplicate route %q", key)
		}
		seen[key] = true

		switch rt.Role {
		case rolePublic, roleViewer, roleAdmin:
		default:
			t.Errorf("route %q has invalid role %q", key, rt.Role)
		}
		// 变更类端点必须审计；GET 不应记审计行。新增只读 POST 时在这里
		// 显式加例外，而不是悄悄把 Audit 设成 false。
		if rt.Method == http.MethodGet && rt.Audit {
			t.Errorf("GET route %q must not be marked as audited", key)
		}
		if rt.Method != http.MethodGet && !rt.Audit {
			t.Errorf("mutation route %q must be marked as audited", key)
		}
		if rt.BodyLimitExempt {
			exempt = append(exempt, rt.Pattern)
		}
	}

	// 公开面必须显式列白：任何新增公开路由都要在这里过一遍，防止
	// Role 漏填把管理端点暴露出去。
	publicAllowed := map[string]bool{
		"/api/admin/health":    true,
		"/speedtest/{size}":    true,
		"/api/transfer/{code}": true,
	}
	for _, rt := range routes {
		if rt.Role == rolePublic && !publicAllowed[rt.Pattern] {
			t.Errorf("route %q is public but not in the explicit public allowlist", rt.PatternWithMethod())
		}
		if rt.Role != rolePublic && publicAllowed[rt.Pattern] {
			t.Errorf("route %q is in the public allowlist but has role %q", rt.PatternWithMethod(), rt.Role)
		}
	}
	for pattern := range publicAllowed {
		if !seen[http.MethodGet+" "+pattern] {
			t.Errorf("public allowlist entry %q has no registered route", pattern)
		}
	}

	// body-limit 豁免以路由表为准，platform 的集合必须与之一致。
	registerBodyLimitExemptions(routes)
	wantExempt := append([]string(nil), exempt...)
	sort.Strings(wantExempt)
	if got := platform.BodyLimitExemptions(); !reflect.DeepEqual(got, wantExempt) {
		t.Errorf("body-limit exemption set drifted:\n got:  %v\n want: %v", got, wantExempt)
	}
	if !reflect.DeepEqual(wantExempt, []string{"/api/admin/transfer/upload"}) {
		t.Errorf("unexpected large-upload exemption set: %v", wantExempt)
	}
}

// TestRouteTableCriticalRoles 对少数安全关键端点做显式断言，避免路由表
// 的 Role 被误改后只靠“自洽”测试发现不了。
func TestRouteTableCriticalRoles(t *testing.T) {
	want := map[string]string{
		"GET /api/admin/users":                     roleViewer,
		"POST /api/admin/users/xray/add":           roleViewer,
		"GET /api/admin/users/xray/config/{uuid}":  roleViewer,
		"POST /api/admin/users/admin/add":          roleAdmin,
		"POST /api/admin/users/admin/delete":       roleAdmin,
		"POST /api/admin/users/roles/set":          roleAdmin,
		"POST /api/admin/services/{name}/{action}": roleAdmin,
		"POST /api/admin/security/block":           roleAdmin,
		"POST /api/admin/transfer/upload":          roleViewer,
		"GET /api/transfer/{code}":                 rolePublic,
		"GET /api/admin/health":                    rolePublic,
	}
	got := map[string]string{}
	for _, rt := range apiRoutes(nil, identity.NewStore(runtime.NewLayout(t.TempDir(), t.TempDir()), nil), nil, transfer.NewStore(runtime.NewLayout(t.TempDir(), t.TempDir())), runtime.NewLayout(t.TempDir(), t.TempDir())) {
		got[rt.PatternWithMethod()] = rt.Role
	}
	for key, role := range want {
		if got[key] != role {
			t.Errorf("%s role = %q, want %q", key, got[key], role)
		}
	}
}
