package httpapi

// 嵌套路径中间字段不存在时，所有运算符都算不匹配（包括 ne 与
// _where 的嵌套对象）。这是 v1 bug（dims.w=2 会返回没有 dims 的记录）的有意修复，
// 0.17 无此行为。数据同 scripts/compare-db3.json
// （stringMatchDB 已内嵌该数据），products 中 id 5 没有 dims 字段。

import "testing"

// TestMissingNestingAcceptance 逐行检查缺失父字段的匹配结果。
func TestMissingNestingAcceptance(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{})
	tests := []struct {
		path string
		ids  []string
	}{
		{"/products?dims.w=2", []string{"2", "4"}},
		{"/products?dims.w=1&dims.w=2", []string{"1", "2", "4", "6"}},
		{"/products?dims.w:eq=2", []string{"2", "4"}},
		{"/products?dims.w:ne=2", []string{"1", "3", "6"}},
		{"/products?dims.w:in=1,2", []string{"1", "2", "4", "6"}},
		{"/products?dims.w_gte=2", []string{"2", "3", "4"}},
		{"/products?dims.w_lte=2", []string{"1", "2", "4", "6"}},
		{"/products?dims.w_ne=2", []string{"1", "3", "6"}},
	}
	for _, tc := range tests {
		status, _, body := doReq(app, "GET", tc.path, "")
		if status != 200 {
			t.Fatalf("%s: 状态 %d (%s)", tc.path, status, body)
		}
		if got := idsOf(t, body); !eqIDs(got, tc.ids) {
			t.Errorf("%s: id %v, 期望 %v", tc.path, got, tc.ids)
		}
	}
	// _where 的嵌套对象同样：_where={"dims":{"w":{"eq":2}}} → [2,4]。
	_, _, body := doReq(app, "GET", "/products?_where=%7B%22dims%22%3A%7B%22w%22%3A%7B%22eq%22%3A2%7D%7D%7D", "")
	if got := idsOf(t, body); !eqIDs(got, []string{"2", "4"}) {
		t.Errorf("_where dims.w eq 2: id %v, 期望 [2 4] (%s)", got, body)
	}
}
