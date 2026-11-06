package query

import (
	"encoding/json"
	"testing"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// mustDocs 解析测试文档片段（数组）。
func mustDocs(t *testing.T, src string) []jsonx.Value {
	t.Helper()
	v, err := jsonx.Parse([]byte(src))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	arr, ok := jsonx.AsArray(v)
	if !ok {
		t.Fatal("测试数据应为数组")
	}
	return arr
}

const docsJSON = `[
  {"id": 1, "name": "Alice", "meta": {"age": 30, "city": "Beijing"}, "score": 95.5, "tags": ["go", "json"], "ok": true, "note": null},
  {"id": 2, "name": "bob", "meta": {"age": 25}, "score": 70, "tags": ["go"], "ok": false},
  {"id": 3, "name": "Cara", "meta": {"age": 40, "city": "Shanghai"}, "tags": []}
]`

func filterIDsIn(t *testing.T, docs []jsonx.Value, m Match) []string {
	t.Helper()
	var ids []string
	for _, d := range docs {
		if m(d) {
			o, _ := jsonx.AsObject(d)
			v, _ := o.Get("id")
			s, _ := jsonx.ToString(v)
			ids = append(ids, s)
		}
	}
	return ids
}

func eqList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// matchWhere 编译 _where JSON 并过滤。
func matchWhere(t *testing.T, docs []jsonx.Value, whereJSON string) []string {
	t.Helper()
	v, err := jsonx.Parse([]byte(whereJSON))
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := jsonx.AsObject(v)
	if !ok {
		t.Fatalf("应为对象: %s", whereJSON)
	}
	m, err := CompileWhere(obj)
	if err != nil {
		t.Fatalf("%s: %v", whereJSON, err)
	}
	return filterIDsIn(t, docs, m)
}

// TestMatchesWhere 对照 v1 matches-where 语义：
// 字段按单段键、=== 严格比较、标量 value 恒假、"or" 特殊处理、嵌套对象递归。
func TestMatchesWhere(t *testing.T) {
	docs := mustDocs(t, docsJSON)
	tests := []struct {
		where string
		want  []string
	}{
		// 标量 value 恒假（含点号形式键、字符串/布尔/null/数组）。
		{`{"name":"Alice"}`, nil},
		{`{"meta.age":30}`, nil},
		{`{"note":null}`, nil},
		{`{"tags":["go"]}`, nil},
		// 运算符对象：严格等值大小写敏感。
		{`{"name":{"eq":"alice"}}`, nil},
		{`{"name":{"eq":"Alice"}}`, []string{"1"}},
		{`{"ok":{"eq":true}}`, []string{"1"}},
		{`{"ok":{"eq":"true"}}`, nil},
		{`{"note":{"eq":null}}`, []string{"1"}},
		{`{"note":{"ne":null}}`, nil}, // 其余记录缺字段不匹配 ne
		{`{"missing":{"lt":1}}`, nil}, // 字段缺失不匹配
		// 关系比较（数字/字符串混合按 ToNumber）。
		{`{"meta":{"age":{"gte":30,"lt":40}}}`, []string{"1"}},
		{`{"id":{"gt":"2"}}`, []string{"3"}},
		{`{"score":{"gte":90}}`, []string{"1"}},
		{`{"views":{"gte":10}}`, nil},
		// in：数组逐项 ===；非数组按单值；字符串不转数字。
		{`{"id":{"in":[1,2]}}`, []string{"1", "2"}},
		{`{"id":{"in":2}}`, []string{"2"}},
		{`{"id":{"in":"1"}}`, nil},
		// contains/startsWith/endsWith 仅字符串且转小写。
		{`{"name":{"contains":"ar"}}`, []string{"3"}},
		{`{"name":{"contains":"AR"}}`, []string{"3"}},
		{`{"name":{"startsWith":"a"}}`, []string{"1"}},
		{`{"name":{"endsWith":"e"}}`, []string{"1"}},
		{`{"ok":{"contains":"tru"}}`, nil}, // 字段不是字符串
		// 递归嵌套：字段不是对象时 continue（条件通过）。
		{`{"meta":{"city":{"eq":"Beijing"}}}`, []string{"1"}},
		{`{"meta":{"tags":{}}}`, []string{"1", "2", "3"}}, // tags 缺失/非对象都通过
		{`{"meta":{"age":30}}`, nil},                      // 递归里标量 value 恒假
		// "or"：空数组/非数组恒假；非对象子项跳过；子项按递归。
		{`{"or":[{"name":{"eq":"Alice"}},{"name":{"eq":"bob"}}]}`, []string{"1", "2"}},
		{`{"or":[]}`, nil},
		{`{"or":"x"}`, nil},
		{`{"or":[{"nope":"x"},{"id":{"eq":1}}]}`, []string{"1"}},
		// 未知运算符键被忽略（knownOps 不含它则按递归分支）。
		{`{"id":{"bogus":1}}`, []string{"1", "2", "3"}},
	}
	for _, tc := range tests {
		got := matchWhere(t, docs, tc.where)
		if !eqList(got, tc.want) {
			t.Errorf("_where %s: got %v, want %v", tc.where, got, tc.want)
		}
	}
}

func TestCompileWhereValue(t *testing.T) {
	// 普通对象 → 可用；标量 → 不可用（调用方回退）；空数组恒真、非空数组恒假。
	v, _ := jsonx.Parse([]byte(`{"a":1}`))
	if _, ok := CompileWhereValue(v); !ok {
		t.Error("对象应为可用")
	}
	v, _ = jsonx.Parse([]byte(`"x"`))
	if _, ok := CompileWhereValue(v); ok {
		t.Error("标量应为不可用")
	}
	docs := mustDocs(t, docsJSON)
	v, _ = jsonx.Parse([]byte(`[]`))
	if m, ok := CompileWhereValue(v); !ok || len(filterIDsIn(t, docs, m)) != 3 {
		t.Error("空数组应恒真")
	}
	v, _ = jsonx.Parse([]byte(`[1]`))
	if m, ok := CompileWhereValue(v); !ok || len(filterIDsIn(t, docs, m)) != 0 {
		t.Error("非空数组应恒假")
	}
}

func TestSplitV1Key(t *testing.T) {
	tests := []struct {
		key      string
		path, op string
		ok       bool
	}{
		{"name", "name", "eq", true},
		{"author.name", "author.name", "eq", true},
		{"id:gte", "id", "gte", true},
		{"id:GTE", "", "", false},       // 表匹配大小写敏感
		{"id:bogus", "", "", false},     // 未知运算符整参丢弃
		{"id:", "id:", "eq", true},      // 冒号后为空按整键 eq
		{"a:b:gte", "a:b", "gte", true}, // lastIndexOf 拆分
		{"views_gte", "views", "gte", true},
		{"title_like", "title_like", "eq", true}, // like 不在运算符表
		{"_gte", "_gte", "eq", true},             // path 为空回退整键
		{"a_b_gte", "a_b", "gte", true},          // 贪婪匹配
	}
	for _, tc := range tests {
		p, o, ok := SplitV1Key(tc.key)
		if ok != tc.ok || (ok && (p != tc.path || o != tc.op)) {
			t.Errorf("SplitV1Key(%q) = %q,%q,%v 期望 %q,%q,%v", tc.key, p, o, ok, tc.path, tc.op, tc.ok)
		}
	}
	if !IsOperator("startsWith") || IsOperator("StartsWith") || IsOperator("like") {
		t.Error("IsOperator 表匹配不符")
	}
}

func TestCoerceParamValue(t *testing.T) {
	tests := []struct {
		in   string
		want string // 期望的 JSON 紧凑形式
	}{
		{"true", "true"}, {"false", "false"}, {"null", "null"},
		{"42", "42"}, {"-2.5", "-2.5"}, {" 42 ", "42"}, {"1e3", "1000"},
		{"0x10", "16"},
		{"", `""`}, {" ", `" "`}, // trim 空保留原串
		{"Infinity", `"Infinity"`}, {"NaN", `"NaN"`}, // Number.isFinite 才转数字
		{"abc", `"abc"`}, {"true false", `"true false"`},
	}
	for _, tc := range tests {
		v := CoerceParamValue(tc.in)
		raw, err := jsonx.MarshalCompact(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Errorf("CoerceParamValue(%q) = %s 期望 %s", tc.in, raw, tc.want)
		}
	}
}

func TestSetParamToWhere(t *testing.T) {
	root := jsonx.NewObject()
	SetParamToWhere(root, "author.name", "eq", "x")
	SetParamToWhere(root, "views", "gte", "10")
	SetParamToWhere(root, "id", "in", "2, 3,x")
	SetParamToWhere(root, "meta.age", "lt", "99")
	// eq/ne/in 的绑定值改为 LooseBound（类型值+参数原串），不再是纯 JSON 值；
	// 其余运算符仍写 Coerce 后的纯 JSON 值。断言逐条检查路径与绑定形态。
	lbOf := func(v jsonx.Value, val jsonx.Value, raw string) bool {
		lb, ok := v.(LooseBound)
		return ok && jsStrictEq(lb.Val, val) && lb.Raw == raw
	}
	if v, ok := GetPathParts(root, []string{"author", "name", "eq"}); !ok || !lbOf(v, "x", "x") {
		t.Errorf("author.name.eq 应为 LooseBound{x,x}，实际 %v", v)
	}
	if v, ok := GetPathParts(root, []string{"views", "gte"}); !ok {
		t.Error("views.gte 缺失")
	} else if n, isNum := v.(json.Number); !isNum || n.String() != "10" {
		t.Errorf("gte 绑定应保持 Coerce 数字: %v", v)
	}
	if v, ok := GetPathParts(root, []string{"id", "in"}); !ok {
		t.Error("id.in 缺失")
	} else {
		elems, isArr := jsonx.AsArray(v)
		if !isArr || len(elems) != 3 {
			t.Fatalf("in 拆分不符: %v", v)
		}
		if !lbOf(elems[0], json.Number("2"), "2") || !lbOf(elems[1], json.Number("3"), "3") || !lbOf(elems[2], "x", "x") {
			t.Errorf("in 元素应为 LooseBound（逐项 trim+coerce）: %v", elems)
		}
	}
	if v, ok := GetPathParts(root, []string{"meta", "age", "lt"}); !ok {
		t.Error("meta.age.lt 缺失")
	} else if n, isNum := v.(json.Number); !isNum || n.String() != "99" {
		t.Errorf("lt 绑定应保持 Coerce 数字: %v", v)
	}
	// dot-prop：路径上既有对象被保留，新键写入其中。
	root2 := jsonx.NewObject()
	SetParamToWhere(root2, "a", "eq", "x")
	SetParamToWhere(root2, "a.b", "eq", "y")
	if v, ok := GetPathParts(root2, []string{"a", "eq"}); !ok || !lbOf(v, "x", "x") {
		t.Errorf("dot-prop a.eq 不符: %v", v)
	}
	if v, ok := GetPathParts(root2, []string{"a", "b", "eq"}); !ok || !lbOf(v, "y", "y") {
		t.Errorf("dot-prop a.b.eq 不符: %v", v)
	}
}

// TestLegacyFieldMatch 对照 0.17 plural.js 过滤语义。
func TestLegacyFieldMatch(t *testing.T) {
	docs := mustDocs(t, `[
	  {"id":1,"title":"Post 1","n":5},
	  {"id":2,"title":"Post 2","n":"7"},
	  {"id":3,"title":"Post 3"},
	  {"id":4,"title":"Post 4","n":null},
	  {"id":5,"title":"post 5","n":0}
	]`)
	tests := []struct {
		key  string
		vals []string
		want []string
	}{
		// 等值：与 toString 结果大小写敏感比较。
		{"title", []string{"Post 1"}, []string{"1"}},
		{"title", []string{"post 1"}, nil},
		{"n", []string{"5"}, []string{"1"}},
		// 多值 OR；_ne 多值 AND。
		{"title", []string{"Post 1", "Post 2"}, []string{"1", "2"}},
		{"n", []string{"5", "7"}, []string{"1", "2"}},
		{"n_ne", []string{"5", "7"}, []string{"5"}},
		{"title_ne", []string{"Post 1", "post 1"}, []string{"2", "3", "4", "5"}},
		{"n_ne", []string{"5"}, []string{"2", "5"}},
		// _gte：param <= element（松散）；_lte：param >= element。
		{"n_gte", []string{"5"}, []string{"1", "2"}},
		{"n_lte", []string{"5"}, []string{"1", "5"}},
		// _like：new RegExp(pattern,"i").test(toString)。
		{"title_like", []string{"Post [12]"}, []string{"1", "2"}},
		// 字段缺失或 null：所有运算符都不匹配（id1 n=5 排除、id3 缺失、id4 null 都不算）。
		// 点号路径断言用下面的独立夹具（lodash get）。
	}
	for _, tc := range tests {
		got := filterIDsIn(t, docs, LegacyFieldMatch(tc.key, tc.vals))
		if !eqList(got, tc.want) {
			t.Errorf("%s=%v: got %v, want %v", tc.key, tc.vals, got, tc.want)
		}
	}
	docs2 := mustDocs(t, `[{"id":1,"meta":{"age":30}},{"id":2,"meta":{"age":25}}]`)
	if got := filterIDsIn(t, docs2, LegacyFieldMatch("meta.age_gte", []string{"30"})); !eqList(got, []string{"1"}) {
		t.Errorf("meta.age_gte: got %v", got)
	}
	if got := filterIDsIn(t, docs2, LegacyFieldMatch("meta.age", []string{"30"})); !eqList(got, []string{"1"}) {
		t.Errorf("meta.age: got %v", got)
	}
}

func TestLegacyHasParam(t *testing.T) {
	docs := mustDocs(t, `[{"id":1,"meta":{"age":3}},{"id":2,"x":null}]`)
	if !LegacyHasParam(docs, "id") || !LegacyHasParam(docs, "meta.age") || !LegacyHasParam(docs, "x") {
		t.Error("存在路径应为真")
	}
	if LegacyHasParam(docs, "nope") || LegacyHasParam(docs, "meta.nope") || LegacyHasParam(docs, "id.x") {
		t.Error("缺失路径应为假")
	}
}

// TestFullTextLegacy 对照 deepQuery：falsy 跳过、
// 数字/布尔经 toString 参与匹配、q 已小写。
func TestFullTextLegacy(t *testing.T) {
	docs := mustDocs(t, `[
	  {"id":1,"title":"Post 1","n":5},
	  {"id":2,"title":"Post 2","n":"7"},
	  {"id":3,"title":"Post 3","n":null},
	  {"id":4,"title":"post 4","arr":[10,11]},
	  {"id":5,"t":false,"deep":{"k":"XyZ"}}
	]`)
	tests := []struct {
		q    string
		want []string
	}{
		{"post", []string{"1", "2", "3", "4"}},
		{"5", []string{"1", "5"}}, // id 字段本身也参与（toString）
		{"7", []string{"2"}},
		{"null", nil},  // null 值 falsy 跳过
		{"false", nil}, // false 值 falsy 跳过
		// deepQuery 数组逐元素递归（"10"/"11" 单独），不做整串拼接。
		{"10,11", nil},
		{"10", []string{"4"}},
		{"xyz", []string{"5"}}, // 嵌套对象递归
		{"", nil},
	}
	for _, tc := range tests {
		got := filterIDsIn(t, docs, FullTextLegacy(tc.q))
		if !eqList(got, tc.want) {
			t.Errorf("q=%q: got %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestJSHelpers(t *testing.T) {
	v := mustDocs(t, `[{"a":1}]`)[0]
	if jsToString(v) != "[object Object]" {
		t.Errorf("对象 toString: %q", jsToString(v))
	}
	arr, _ := jsonx.Parse([]byte(`[1,null,"x",[2]]`))
	if jsToString(arr) != "1,,x,2" {
		t.Errorf("数组 toString: %q", jsToString(arr))
	}
	if jsToString(nil) != "null" || !jsStrictEq(nil, nil) || jsStrictEq(nil, false) {
		t.Error("null 语义不符")
	}
	one, _ := jsonx.Parse([]byte(`1`))
	oneB, _ := jsonx.Parse([]byte(`1`))
	sOne := jsonx.Value("1")
	if !jsStrictEq(one, oneB) || jsStrictEq(one, sOne) {
		t.Error("=== 数字与字符串不应相等")
	}
	// 关系比较：字符串对字符串按码位；否则 ToNumber；NaN 时 ok=false。
	if c, ok := jsRelCmp(jsonx.Value("a"), jsonx.Value("b")); !ok || c != -1 {
		t.Error("字符串比较不符")
	}
	if c, ok := jsRelCmp(one, jsonx.Value("2")); !ok || c != -1 {
		t.Error("混合 ToNumber 比较不符")
	}
	if _, ok := jsRelCmp(jsonx.Value("abc"), one); ok {
		t.Error("NaN 比较应 ok=false")
	}
	if f, ok := jsToNumber(nil); !ok || f != 0 {
		t.Error("null ToNumber 应为 0")
	}
	// JS 真值：非空字符串（含 "0"/"false"）为真，空串为假。
	if !jsTruthy(jsonx.Value("0")) || jsTruthy(jsonx.Value("")) || !jsTruthy(jsonx.Value("false")) {
		t.Error("jsTruthy 字符串规则不符")
	}
}

func TestAlloAnyOfHelpers(t *testing.T) {
	docs := mustDocs(t, docsJSON)
	if !Always()(docs[0]) {
		t.Error("Always 应为真")
	}
	if AnyOf()(docs[0]) {
		t.Error("AnyOf() 应为假")
	}
	if !AllOf()(docs[0]) {
		t.Error("AllOf() 应为真")
	}
}
