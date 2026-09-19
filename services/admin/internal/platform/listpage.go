package platform

import (
	"net/http"
	"strconv"
)

// 列表分页参数的统一解析:limit/offset 均为可选,默认返回全量(与历史行为
// 兼容,前端暂未接入分页 UI);显式传入时严格校验——limit 上限 1000 防单次
// 全量拖垮面板,offset 非负。非法值一律 400 并写明原因,不静默回退。
const (
	listMaxLimit = 1000
)

// ListPage 是解析后的分页参数。Limit<0 表示"未分页,返回全量"。
type ListPage struct {
	Limit  int
	Offset int
}

// ParseListPage 从 query 解析 limit/offset。返回 ok=false 时已写好 400 响应。
func ParseListPage(w http.ResponseWriter, r *http.Request) (ListPage, bool) {
	p := ListPage{Limit: -1}
	q := r.URL.Query()
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > listMaxLimit {
			WriteError(w, "limit 必须是 1–1000 的整数", http.StatusBadRequest)
			return p, false
		}
		p.Limit = n
	}
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			WriteError(w, "offset 必须是非负整数", http.StatusBadRequest)
			return p, false
		}
		p.Offset = n
	}
	return p, true
}

// Apply 对已排序的切片做 offset/limit 切片(Limit<0 原样返回)。泛型约束下
// 切片拷贝引用元素,调用方保证元素不可变或接受共享。
func Apply[T any](items []T, p ListPage) []T {
	if p.Limit < 0 && p.Offset == 0 {
		return items
	}
	if p.Offset >= len(items) {
		return []T{}
	}
	end := len(items)
	if p.Limit >= 0 && p.Offset+p.Limit < end {
		end = p.Offset + p.Limit
	}
	out := make([]T, end-p.Offset)
	copy(out, items[p.Offset:end])
	return out
}
