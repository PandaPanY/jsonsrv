package query

// 对忠实移植的 v1 JS 语义助手与 MatchesWhere/applyWhereOp 直接单测。
// 断言的是与原版对照后确定的行为，不是迁就实现的产物。

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// mustValue 解析单个 JSON 片段为 jsonx 值。
func mustValue(t *testing.T, src string) jsonx.Value {
	t.Helper()
	v, err := jsonx.Parse([]byte(src))
	if err != nil {
		t.Fatalf("解析失败 %s: %v", src, err)
	}
	return v
}

// mustObj 解析为普通对象。
func mustObj(t *testing.T, src string) *jsonx.Object {
	t.Helper()
	o, ok := jsonx.AsObject(mustValue(t, src))
	if !ok {
		t.Fatalf("应为对象: %s", src)
	}
	return o
}

func TestMatchesWhereJSObject(t *testing.T) {
	cases := []struct {
		rec, where string
		want       bool
	}{
		// 标量或数组 value：恒假（matches-where 源码 lines 82-84）。
		{`{"id": 1}`, `{"id": 1}`, false},
		{`{"id": 1}`, `{"id": [1]}`, false},
		// or：非数组/空数组恒假；任一子对象匹配为真；非对象子项跳过。
		{`{"id": 1}`, `{"or": []}`, false},
		{`{"id": 1}`, `{"or": {"eq": 1}}`, false},
		{`{"id": 1}`, `{"or": [{"id": {"eq": 1}}]}`, true},
		{`{"id": 1}`, `{"or": [5, {"id": {"eq": 1}}]}`, true},
		{`{"id": 1}`, `{"or": [{"id": {"eq": 2}}, {"id": {"eq": 1}}]}`, true},
		{`{"id": 1}`, `{"or": [{"id": {"eq": 2}}]}`, false},
		// 含已知运算符的对象：字段缺失恒假；全部运算符须通过。
		{`{"id": 1}`, `{"missing": {"eq": 1}}`, false},
		{`{"id": 1}`, `{"id": {"eq": 1}}`, true},
		{`{"id": 1}`, `{"id": {"eq": 1, "ne": 2}}`, true},
		{`{"id": 1}`, `{"id": {"eq": 1, "ne": 1}}`, false},
		// 不含已知运算符的对象：字段为对象时递归；字段不是对象时，子树里有运算符就不匹配
		// （v1 在这里放行，是 bug），子树没有运算符则该条件通过。
		{`{"author": {"name": "a"}}`, `{"author": {"name": {"eq": "a"}}}`, true},
		{`{"author": "s"}`, `{"author": {"x": {"eq": 1}}}`, false},
		{`{"author": null}`, `{"author": {"x": {"eq": 1}}}`, false},
		{`{"id": 1}`, `{"author": {"x": {"eq": 1}}}`, false},
		{`{"author": "s"}`, `{"author": {"x": {}}}`, true},
		{`{"author": {"name": "a"}}`, `{"author": {"name": "a"}}`, false},
	}
	for _, c := range cases {
		if got := MatchesWhere(mustValue(t, c.rec), mustObj(t, c.where)); got != c.want {
			t.Errorf("MatchesWhere(%s, %s) = %v, 期望 %v", c.rec, c.where, got, c.want)
		}
	}
}

func TestApplyWhereOps(t *testing.T) {
	cases := []struct {
		field, bound, op string
		want             bool
	}{
		{`3`, `5`, "lt", true},
		{`3`, `"a"`, "lte", false}, // 双方字符串才按字符串；否则 ToNumber → NaN 恒假
		{`"a"`, `"b"`, "lte", true},
		{`5`, `3`, "gt", true},
		{`3`, `3`, "gte", true},
		{`"3"`, `3`, "eq", false}, // === 类型敏感
		{`"3"`, `3`, "ne", true},
		{`null`, `null`, "eq", true},
		{`["x"]`, `["x"]`, "eq", false}, // 对象/数组引用恒不等
		{`"x"`, `"x"`, "in", true},      // 非数组 bound 按单元素
		{`"y"`, `["x", "y"]`, "in", true},
		{`"y"`, `["x"]`, "in", false},
		{`"Hello"`, `"hel"`, "contains", true}, // 大小写归一
		{`5`, `"5"`, "contains", false},        // 字段非字符串恒假
		{`"Hello"`, `"HE"`, "startsWith", true},
		{`"Hello"`, `"LO"`, "endsWith", true},
		{`1`, `1`, "regex", true}, // 未知运算符名不参与（由调用方过滤），此处恒真
	}
	for _, c := range cases {
		if got := applyWhereOp(mustValue(t, c.field), c.op, mustValue(t, c.bound)); got != c.want {
			t.Errorf("applyWhereOp(%s, %s, %s) = %v, 期望 %v", c.field, c.op, c.bound, got, c.want)
		}
	}
}

func TestJSSemanticHelpers(t *testing.T) {
	if jsToString(nil) != "null" || jsToString(true) != "true" || jsToString(json.Number("1.5")) != "1.5" {
		t.Error("jsToString 原始值不合法")
	}
	if got := jsToString(mustValue(t, `["a", null, ["b", "c"]]`)); got != "a,,b,c" {
		t.Errorf("jsToString 数组应按 join(\",\")，null 元素为空串: %q", got)
	}
	if got := jsToString(mustObj(t, `{}`)); got != "[object Object]" {
		t.Errorf("jsToString 对象: %q", got)
	}

	truthy := map[string]bool{
		`0`:     false,
		`1`:     true,
		`""`:    false,
		`"0"`:   true,
		`false`: false,
		`true`:  true,
		`null`:  false,
		`[]`:    true,
		`{}`:    true,
	}
	for src, want := range truthy {
		if got := jsTruthy(mustValue(t, src)); got != want {
			t.Errorf("jsTruthy(%s) = %v, 期望 %v", src, got, want)
		}
	}

	num := []struct {
		src    string
		want   float64
		wantOK bool
	}{
		{`null`, 0, true},
		{`true`, 1, true},
		{`false`, 0, true},
		{`"42"`, 42, true},
		{`"1e5"`, 1e5, true},
		{`" 0x1A "`, 26, true},
		{`"Infinity"`, math.Inf(1), true},
		{`"-Infinity"`, math.Inf(-1), true},
		{`""`, 0, true},
		{"\"  \"", 0, true},
		{`"abc"`, 0, false},
		{`"12x"`, 0, false},
		{`"1e"`, 0, false},
		{`[]`, 0, false},
		{`{}`, 0, false},
	}
	for _, c := range num {
		got, ok := jsToNumber(mustValue(t, c.src))
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("jsToNumber(%s) = (%v,%v), 期望 (%v,%v)", c.src, got, ok, c.want, c.wantOK)
		}
	}

	// 关系比较：双方字符串按字符串（码位序），否则双方 ToNumber；NaN 一律假。
	if c, ok := jsRelCmp(mustValue(t, `"a"`), mustValue(t, `"b"`)); !ok || c != -1 {
		t.Errorf("jsRelCmp 字符串: (%v,%v)", c, ok)
	}
	if c, ok := jsRelCmp(mustValue(t, `"10"`), mustValue(t, `9`)); !ok || c != 1 {
		t.Errorf("jsRelCmp 数字串应 ToNumber（JS \"10\" > 9）: (%v,%v)", c, ok)
	}
	if _, ok := jsRelCmp(mustValue(t, `"a"`), mustValue(t, `1`)); ok {
		t.Error("jsRelCmp NaN 应 ok=false")
	}
	if !jsStrictEq(mustValue(t, `null`), mustValue(t, `null`)) ||
		jsStrictEq(mustValue(t, `"1"`), mustValue(t, `1`)) ||
		!jsStrictEq(mustValue(t, `1`), mustValue(t, `1`)) {
		t.Error("jsStrictEq 原始值语义不符")
	}
}

func TestCompileLikeReFallback(t *testing.T) {
	re := compileLikeRe("a.c")
	if re == nil || !re.MatchString("xaxc") {
		t.Error("合法模式应按正则匹配（忽略大小写）")
	}
	// 非法 JS 正则退化为字面量匹配（：原版会 500，此处降级为有意差异）。
	lit := compileLikeRe("[a-z")
	if lit == nil {
		t.Fatal("两级编译不应同时失败")
	}
	if !lit.MatchString("x[a-z") || lit.MatchString("xaz") {
		t.Error("非法模式应退化为字面量匹配")
	}
}

func TestSplitV1KeyAndParams(t *testing.T) {
	cases := []struct {
		key, path, op string
		ok            bool
	}{
		{"id", "id", "eq", true},
		{"id:eq", "id", "eq", true},
		{"id:in", "id", "in", true},
		{"id:bogus", "", "", false},
		{"id:", "id:", "eq", true},
		{"id_lte", "id", "lte", true},
		{"id_x", "id_x", "eq", true},
	}
	for _, c := range cases {
		p, op, ok := SplitV1Key(c.key)
		if ok != c.ok {
			t.Errorf("SplitV1Key(%s) ok=%v, 期望 %v", c.key, ok, c.ok)
			continue
		}
		if ok && (p != c.path || op != c.op) {
			t.Errorf("SplitV1Key(%s) = (%s,%s), 期望 (%s,%s)", c.key, p, op, c.path, c.op)
		}
	}

	if v, ok := CoerceParamValue("42").(json.Number); !ok || v.String() != "42" {
		t.Errorf("CoerceParamValue 数字: %v", CoerceParamValue("42"))
	}
	if CoerceParamValue("true") != true || CoerceParamValue("null") != nil {
		t.Error("CoerceParamValue 布尔/null 转换不符")
	}
	if s, ok := CoerceParamValue("Infinity").(string); !ok || s != "Infinity" {
		t.Error("CoerceParamValue Infinity 应保持原串（Number.isFinite）")
	}
	if s, ok := CoerceParamValue("  ").(string); !ok || s != "  " {
		t.Error("CoerceParamValue 纯空白应返回原串")
	}
	if v, ok := CoerceParamValue("42.5").(json.Number); !ok || v.String() != "42.5" {
		t.Error("CoerceParamValue 小数不符")
	}

	root := jsonx.NewObject()
	SetParamToWhere(root, "a.b", "eq", "42")
	if v, ok := GetPathParts(root, []string{"a", "b", "eq"}); !ok {
		t.Error("点号路径应嵌套写入")
	} else if lb, isLB := v.(LooseBound); !isLB {
		t.Errorf("eq 绑定应为 LooseBound: %v", v)
	} else if n, isNum := lb.Val.(json.Number); !isNum || n.String() != "42" || lb.Raw != "42" {
		t.Errorf("嵌套写入值不符: %v", v)
	}
	SetParamToWhere(root, "id", "in", "1, 2,x")
	arr, ok := GetPathParts(root, []string{"id", "in"})
	if !ok {
		t.Fatal("in 应拆分为数组")
	}
	elems, isArr := jsonx.AsArray(arr)
	if !isArr || len(elems) != 3 {
		t.Fatalf("in 拆分不符: %v", arr)
	}
	lb0, ok0 := elems[0].(LooseBound)
	if !ok0 {
		t.Error("in 元素应为 LooseBound")
	} else if _, isNum := lb0.Val.(json.Number); !isNum {
		t.Error("in 元素应逐项 coerce")
	}
	if lb2, ok2 := elems[2].(LooseBound); !ok2 {
		t.Error("in 元素应为 LooseBound")
	} else if _, isStr := lb2.Val.(string); !isStr {
		t.Error("in 非数字元素应保持字符串")
	}
	if _, ok := GetPathParts(mustObj(t, `{"a": 1}`), []string{"a", "b"}); ok {
		t.Error("路径中非对象节点应判定为缺失")
	}
}

func TestCompileRelMixedTypes(t *testing.T) {
	// bound 为字符串：字段也是字符串才按字符串比较，否则双方 ToNumber。
	cmpS := compileRel(mustValue(t, `"5"`))
	if c, ok := cmpS(mustValue(t, `"3"`)); !ok || c != -1 {
		t.Errorf("双方字符串按码位序: (%v,%v)", c, ok)
	}
	if c, ok := cmpS(mustValue(t, `10`)); !ok || c != 1 { // "5" ToNumber=5 < 10
		t.Errorf("一侧字符串一侧数字应 ToNumber: (%v,%v)", c, ok)
	}
	// bound 为数字：字符串字段按 ToNumber（JS "5" < 10 为 true）。
	cmpN := compileRel(mustValue(t, `10`))
	if c, ok := cmpN(mustValue(t, `"5"`)); !ok || c != -1 {
		t.Errorf("JS 语义 \"5\" < 10: (%v,%v)", c, ok)
	}
	if _, ok := cmpN(mustValue(t, `"a"`)); ok {
		t.Error("NaN 比较应 ok=false")
	}
}
