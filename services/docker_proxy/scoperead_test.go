package main

import (
	"net/url"
	"testing"
)

// TestReadScopeOK 锁定 handle-less 读端点的作用域要求:不带允许项目的 label
// 过滤即拒绝(fail-closed),两种 filters JSON 形态都要认——只认一种会让
// docker SDK(map 形态)或手工 curl(数组形态)之一被误拒。
func TestReadScopeOK(t *testing.T) {
	sdkFilters := url.QueryEscape(`{"label":{"com.docker.compose.project=workwork":true,"com.docker.compose.service=xray":true}}`)
	legacyFilters := url.QueryEscape(`{"label":["com.docker.compose.project=workwork","com.docker.compose.service=xray"]}`)
	foreignFilters := url.QueryEscape(`{"label":{"com.docker.compose.project=media":true}}`)
	emptyFilters := url.QueryEscape(`{}`)

	cases := []struct {
		name              string
		method, path, raw string
		want              bool
	}{
		{"非读端点不受约束", "POST", "/containers/abc/start", "", true},
		{"容器级读端点不受约束", "GET", "/containers/abc/json", "", true},
		{"info/version/ping 不受约束", "GET", "/info", "", true},
		{"列表缺 filters", "GET", "/containers/json", "all=1", false},
		{"列表 filters 为空对象", "GET", "/containers/json", "filters=" + emptyFilters, false},
		{"列表 filters 非法 JSON", "GET", "/containers/json", "filters=%7Bbroken", false},
		{"列表只过滤别的项目", "GET", "/containers/json", "filters=" + foreignFilters, false},
		{"事件缺 filters", "GET", "/events", "", false},
		{"列表带 SDK map 形态项目过滤", "GET", "/containers/json", "all=1&filters=" + sdkFilters, true},
		{"列表带 legacy 数组形态项目过滤", "GET", "/containers/json", "filters=" + legacyFilters, true},
		{"事件带项目过滤", "GET", "/v1.47/events", "filters=" + sdkFilters, true},
		{"带版本前缀的列表同样受约束", "GET", "/v1.47/containers/json", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := readScopeOK(c.method, c.path, c.raw); got != c.want {
				t.Errorf("readScopeOK(%s %s ?%s) = %v, want %v", c.method, c.path, c.raw, got, c.want)
			}
		})
	}
}

// TestReadScopeOKHonoursConfiguredProjects: ALLOWED_COMPOSE_PROJECTS 解析出的
// 集合是唯一真相,自定义项目名同样放行(与 compose 接线 COMPOSE_PROJECT_NAME
// 配套——否则改项目名后 admin 的过滤会被整体拒绝)。
func TestReadScopeOKHonoursConfiguredProjects(t *testing.T) {
	orig := allowedProjects
	allowedProjects = map[string]bool{"workwork": true, "media": true}
	t.Cleanup(func() { allowedProjects = orig })

	raw := url.QueryEscape(`{"label":{"com.docker.compose.project=media":true}}`)
	if !readScopeOK("GET", "/containers/json", "filters="+raw) {
		t.Fatal("配置集合内的项目应放行")
	}
}

func TestFilterValues(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"缺失", "", nil},
		{"数组形态", `["a=1","b=2"]`, []string{"a=1", "b=2"}},
		{"map 形态只取 true", `{"a=1":true,"b=2":false}`, []string{"a=1"}},
		{"非字符串标量", `42`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filterValues([]byte(c.raw))
			if len(got) != len(c.want) {
				t.Fatalf("filterValues(%s) = %v, want %v", c.raw, got, c.want)
			}
			set := map[string]bool{}
			for _, v := range got {
				set[v] = true
			}
			for _, w := range c.want {
				if !set[w] {
					t.Errorf("filterValues(%s) 缺少 %q (得到 %v)", c.raw, w, got)
				}
			}
		})
	}
}
