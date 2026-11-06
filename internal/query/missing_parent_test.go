package query

// CompileMatch 与 MatchesWhere 对「无运算符嵌套对象且中间字段
// 缺失」必须一致地判为不匹配。

import "testing"

func TestMissingNestingBothEngines(t *testing.T) {
	rec := mustObj(t, `{"id":5,"name":"x"}`) // 没有 dims
	where := mustObj(t, `{"dims":{"w":{"eq":2}}}`)
	if CompileMatch(where)(rec) {
		t.Error("CompileMatch：中间字段缺失时 eq 不应匹配")
	}
	if MatchesWhere(rec, where) {
		t.Error("MatchesWhere：中间字段缺失时 eq 不应匹配")
	}
	whereNe := mustObj(t, `{"dims":{"w":{"ne":2}}}`)
	if CompileMatch(whereNe)(rec) {
		t.Error("CompileMatch：中间字段缺失时 ne 不应匹配")
	}
	if MatchesWhere(rec, whereNe) {
		t.Error("MatchesWhere：中间字段缺失时 ne 不应匹配")
	}
	whereGte := mustObj(t, `{"dims":{"w":{"gte":0}}}`)
	if CompileMatch(whereGte)(rec) {
		t.Error("CompileMatch：中间字段缺失时 gte 不应匹配")
	}
	if MatchesWhere(rec, whereGte) {
		t.Error("MatchesWhere：中间字段缺失时 gte 不应匹配")
	}
	// 中间字段存在时正常判定不受影响。
	rec2 := mustObj(t, `{"id":2,"dims":{"w":2}}`)
	if !CompileMatch(where)(rec2) || !MatchesWhere(rec2, where) {
		t.Error("dims.w=2 的记录应匹配 eq 2")
	}
}
