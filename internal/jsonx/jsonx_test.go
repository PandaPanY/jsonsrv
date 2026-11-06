package jsonx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePreservesKeyOrder(t *testing.T) {
	v, err := Parse([]byte(`{"b":1,"a":{"x":true,"y":null},"c":[1,"2",[3]]}`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	obj, ok := AsObject(v)
	if !ok {
		t.Fatal("应解析为对象")
	}
	want := []string{"b", "a", "c"}
	got := obj.Keys()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("键顺序应为 %v, 得到 %v", want, got)
	}
	nested, _ := AsObject(obj.MustGet("a"))
	if nested.MustGet("x") != true {
		t.Fatal("嵌套值应为 true")
	}
	if nested.MustGet("y") != nil {
		t.Fatal("null 应为 nil")
	}
	arr, _ := AsArray(obj.MustGet("c"))
	if len(arr) != 3 {
		t.Fatalf("数组长度错误: %v", arr)
	}
	n, ok := AsNumber(arr[0])
	if !ok || n.String() != "1" {
		t.Fatalf("应为数字 1: %v", arr[0])
	}
	if arr[1] != "2" {
		t.Fatalf("应为字符串 \"2\": %v", arr[1])
	}
}

func TestParseNumberRawFormat(t *testing.T) {
	v, err := Parse([]byte(`{"n":1.50,"big":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(*Object)
	if got := obj.MustGet("n").(json.Number).String(); got != "1.50" {
		t.Fatalf("数字应保留原始格式 1.50, 得到 %s", got)
	}
	if got := obj.MustGet("big").(json.Number).String(); got != "9007199254740993" {
		t.Fatalf("大整数应保留原样, 得到 %s", got)
	}
}

func TestParseDuplicateKeyKeepsFirstPosition(t *testing.T) {
	v, err := Parse([]byte(`{"a":1,"b":2,"a":3}`))
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(*Object)
	if obj.Len() != 2 {
		t.Fatalf("重复键应合并: %d", obj.Len())
	}
	keys := obj.Keys()
	if keys[0] != "a" {
		t.Fatalf("重复键位置应保持首次出现: %v", keys)
	}
	if obj.MustGet("a") != json.Number("3") {
		t.Fatalf("重复键应取后者: %v", obj.MustGet("a"))
	}
}

// TestParseBOM 验证开头的 UTF-8 BOM 自动跳过。
func TestParseBOM(t *testing.T) {
	for _, in := range []string{
		"\ufeff{}",
		"\ufeff{\"a\":1}",
		"\ufeff[1]",
	} {
		if _, err := Parse([]byte(in)); err != nil {
			t.Errorf("BOM 输入 %q 应可解析: %v", in, err)
		}
	}
	v, err := Parse([]byte("\ufeff{\"a\":1}"))
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := AsObject(v)
	if !ok || obj.MustGet("a") != json.Number("1") {
		t.Fatalf("BOM 后内容解析错误: %v", v)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{`{"a":1} extra`, ``, `{"a":`, `[1,]`, `{"a" 1}`, `nul`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Fatalf("输入 %q 应报错", in)
		}
	}
}

func TestParseScalars(t *testing.T) {
	for in, want := range map[string]any{
		`null`: nil,
		`true`: true,
		`"s"`:  "s",
		`123`:  "123", // json.Number 的 String()
	} {
		v, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if in == `null` {
			if v != nil {
				t.Fatalf("%s 应为 nil", in)
			}
			continue
		}
		switch w := want.(type) {
		case bool:
			if v != w {
				t.Fatalf("%s 应为 true", in)
			}
		case string:
			n, ok := AsNumber(v)
			if ok {
				if n.String() != w {
					t.Fatalf("%s 应为 %s", in, w)
				}
			} else if v != w {
				t.Fatalf("%s 应为 %q", in, w)
			}
		}
	}
}

func TestMarshalIndentPresOrder(t *testing.T) {
	obj := NewObject()
	obj.Set("b", json.Number("1"))
	inner := NewObject()
	obj.Set("a", inner)
	data, err := Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"b\": 1,\n  \"a\": {}\n}\n"
	if string(data) != want {
		t.Fatalf("序列化结果错误:\n得到 %q\n期望 %q", data, want)
	}
}

// MarshalIndent 流式输出必须与紧凑序列化 + json.Indent逐字节一致。
func TestMarshalIndentStreamingMatchesIndent(t *testing.T) {
	docs := []string{
		`{}`,
		`{"a":1}`,
		`{"a":{},"b":[],"c":[[1,2],{"d":[3]}]}`,
		`{"s":"带<b>与\"转义\"的文本\n换行"}`,
		`{"n":1.50,"e":1e5,"t":true,"z":null}`,
		`{"deep":{"a":{"b":{"c":[1,{"d":{}}]}}}}`,
		`[[[[]]],[{}]]`,
	}
	for _, doc := range docs {
		v, err := Parse([]byte(doc))
		if err != nil {
			t.Fatalf("解析 %s: %v", doc, err)
		}
		compact, err := MarshalCompact(v)
		if err != nil {
			t.Fatal(err)
		}
		var want []byte
		var buf bytes.Buffer
		if err := json.Indent(&buf, compact, "", "  "); err != nil {
			t.Fatal(err)
		}
		buf.WriteByte('\n')
		want = buf.Bytes()
		got, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("流式输出与 json.Indent 不一致（输入 %s）:\n得到 %q\n期望 %q", doc, got, want)
		}
	}
}

// ShallowClone 只复制顶层，嵌套值共享引用；副本 Set 不影响原对象。
func TestShallowCloneSharesNested(t *testing.T) {
	orig, _ := Parse([]byte(`{"posts":[{"id":1}],"cfg":{"a":1}}`))
	o := orig.(*Object)
	cp := o.ShallowClone()
	if cp.Len() != o.Len() {
		t.Fatal("顶层键数应一致")
	}
	// 嵌套值共享：同一底层对象。
	if _, ok := jsonxSame(o.MustGet("cfg"), cp.MustGet("cfg")); !ok {
		t.Fatal("嵌套值应共享引用")
	}
	// Set 替换顶层键：原对象不受影响。
	cp.Set("cfg", NewObject())
	if v := o.MustGet("cfg"); v == cp.MustGet("cfg") {
		t.Fatal("副本 Set 不应换掉原对象的顶层键")
	}
	// Delete 同样只作用于副本。
	cp.Delete("posts")
	if _, ok := o.Get("posts"); !ok {
		t.Fatal("副本 Delete 不应影响原对象")
	}
}

func jsonxSame(a, b Value) (Value, bool) {
	return a, a == b
}

func TestMarshalNumberKeepsRaw(t *testing.T) {
	v, _ := Parse([]byte(`{"n":1.50,"e":1e5}`))
	data, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\n  \"n\": 1.50,\n  \"e\": 1e5\n}\n" {
		t.Fatalf("数字应原样输出: %s", data)
	}
}

// appendJSONString（经 marshalString/marshalValue）必须与
// encoding/json（SetEscapeHTML(false)）的字符串编码逐字节一致。
func TestMarshalStringMatchesStdEncoding(t *testing.T) {
	cases := []string{
		"简单 ascii",
		"带\"引号\\和\t制表\n换行\r回车",
		"\x00\x01\x1f\x7f 控制符",
		"HTML <b>&\"ok\"</b>",
		"Unicode 你好 — U+2028 \u2029",
		" Kelvin ",
		"截断 e4 b8 残",
		"混\xfe\xff合",
		"",
		"a",
	}
	for _, s := range cases {
		var std bytes.Buffer
		enc := json.NewEncoder(&std)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(s); err != nil {
			t.Fatal(err)
		}
		want := strings.TrimRight(std.String(), "\n")
		got, err := marshalString(s)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("字符串编码与标准库不一致 %q:\n得到 %s\n期望 %s", s, got, want)
		}
	}
}

func TestMarshalNoHTMLEscape(t *testing.T) {
	obj := NewObject()
	obj.Set("s", "a<b>&c")
	data, err := MarshalCompact(obj)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"s":"a<b>&c"}` {
		t.Fatalf("不应转义 HTML 字符: %s", data)
	}
}

func TestMarshalUnsupportedType(t *testing.T) {
	if _, err := MarshalCompact(Value(make(chan int))); err == nil {
		t.Fatal("不支持的类型应报错")
	}
}

func TestObjectSetGetDelete(t *testing.T) {
	o := NewObject()
	o.Set("x", 1)
	o.Set("y", 2)
	if _, ok := o.Get("x"); !ok {
		t.Fatal("x 应存在")
	}
	o.Set("x", 3) // 更新保持位置
	if o.Keys()[0] != "x" {
		t.Fatal("更新应保持原位置")
	}
	if !o.Delete("x") || o.Delete("x") {
		t.Fatal("删除语义不正确")
	}
	if len(o.Keys()) != 1 || o.Keys()[0] != "y" {
		t.Fatalf("删除后键错误: %v", o.Keys())
	}
	if o.MustGet("missing") != nil {
		t.Fatal("MustGet 缺失应为 nil")
	}
}

func TestCloneAndDeepEqualAndDeepEqualClone(t *testing.T) {
	v, _ := Parse([]byte(`{"a":[1,{"b":"x"}],"n":1.50}`))
	clone := DeepClone(v)
	if !DeepEqual(v, clone) {
		t.Fatal("深拷贝应相等")
	}
	o1 := v.(*Object)
	o2 := clone.(*Object)
	arr1, _ := AsArray(o1.MustGet("a"))
	arr2, _ := AsArray(o2.MustGet("a"))
	if &arr1[0] == &arr2[0] {
		t.Fatal("拷贝数组应独立")
	}
	// 修改拷贝不应影响原值
	o2.Set("a", []Value{})
	if DeepEqual(v, o2) {
		t.Fatal("拷贝修改应与原值独立")
	}
	if DeepEqual(v, ParseMust(`{"a":[1,{"b":"y"}]}`)) {
		t.Fatal("DeepEqual 应比较完整结构")
	}
	if !DeepEqual(ParseMust(`{"n":1.50}`), ParseMust(`{"n":1.50}`)) {
		t.Fatal("数字原始格式比较应相等")
	}
	if DeepEqual(ParseMust(`{"n":1.50}`), ParseMust(`{"n":1.5}`)) {
		t.Fatal("不同格式的原始数字视为不同（保守策略）")
	}
}

func TestToString(t *testing.T) {
	if s, ok := ToString(json.Number("1e2")); !ok || s != "1e2" {
		t.Fatal("数字应可转字符串")
	}
	if s, ok := ToString(nil); !ok || s != "" {
		t.Fatal("nil 应转为空串")
	}
	if _, ok := ToString(NewObject()); ok {
		t.Fatal("对象不应转字符串")
	}
}

func TestObjectMarshalUnmarshalRoundTrip(t *testing.T) {
	src := `{"z":1,"y":2,"x":3}`
	var o Object
	if err := json.Unmarshal([]byte(src), &o); err != nil {
		t.Fatal(err)
	}
	keys := o.Keys()
	if keys[0] != "z" || keys[1] != "y" || keys[2] != "x" {
		t.Fatalf("UnmarshalJSON 应保留顺序: %v", keys)
	}
	data, err := json.Marshal(&o)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != src {
		t.Fatalf("round trip 不一致: %s", data)
	}
	if err := json.Unmarshal([]byte(`[1]`), &o); err == nil {
		t.Fatal("非对象应报错")
	}
}

// ParseMust 在测试里解析 JSON，失败直接 panic。
func ParseMust(s string) Value {
	v, err := Parse([]byte(s))
	if err != nil {
		panic(err)
	}
	return v
}
