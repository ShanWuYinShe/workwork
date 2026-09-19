package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseListPage(t *testing.T) {
	mk := func(query string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/list"+query, nil)
		return r
	}
	t.Run("默认全量", func(t *testing.T) {
		p, ok := ParseListPage(httptest.NewRecorder(), mk(""))
		if !ok || p.Limit != -1 || p.Offset != 0 {
			t.Fatalf("p=%+v ok=%v, want Limit=-1", p, ok)
		}
	})
	t.Run("合法 limit/offset", func(t *testing.T) {
		p, ok := ParseListPage(httptest.NewRecorder(), mk("?limit=10&offset=5"))
		if !ok || p.Limit != 10 || p.Offset != 5 {
			t.Fatalf("p=%+v ok=%v", p, ok)
		}
	})
	bad := []string{"?limit=0", "?limit=-1", "?limit=1001", "?limit=abc", "?offset=-1", "?offset=x"}
	for _, q := range bad {
		w := httptest.NewRecorder()
		if _, ok := ParseListPage(w, mk(q)); ok {
			t.Errorf("%s should be rejected", q)
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", q, w.Code)
		}
	}
}

func TestListPageApply(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	full := Apply(items, ListPage{Limit: -1})
	if len(full) != 5 {
		t.Fatalf("full = %v", full)
	}
	got := Apply(items, ListPage{Limit: 2, Offset: 1})
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("paged = %v, want [2 3]", got)
	}
	if out := Apply(items, ListPage{Limit: 10, Offset: 3}); len(out) != 2 || out[0] != 4 {
		t.Fatalf("tail = %v", out)
	}
	if out := Apply(items, ListPage{Limit: 5, Offset: 100}); len(out) != 0 {
		t.Fatalf("beyond-end = %v", out)
	}
	// 确认切片是拷贝:改输出不影响原切片
	out := Apply(items, ListPage{Limit: 2})
	out[0] = 999
	if items[0] != 1 {
		t.Fatal("Apply must return a copy")
	}
}
