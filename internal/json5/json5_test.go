package json5

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// mustUnmarshal 在测试里把 JSON 解为 any（UseNumber），失败直接 panic。
func mustUnmarshal(t *testing.T, data []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("转换结果不是合法 JSON: %v\n内容: %s", err, data)
	}
	return v
}

func TestToJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"行注释", "{\n// 这是注释\n\"a\": 1\n}", `{"a": 1}`},
		{"块注释", "{\"a\": 1 /* 说明 */, \"b\": 2}", `{"a": 1, "b": 2}`},
		{"注释后键", "{a /*x*/ : 1}", `{"a": 1}`},
		{"尾随逗号对象", "{\"a\":1,}", `{"a":1}`},
		{"尾随逗号数组", "[1,2,]", "[1,2]"},
		{"嵌套尾随逗号", "[1,[2,],]", "[1,[2]]"},
		{"尾随逗号后空白", "[1, ]", "[1]"},
		{"单引号", "{'a': 'b'}", `{"a":"b"}`},
		{"单引号转义", "'it\\'s'", `"it's"`},
		{"单引号含双引号", "'say \"hi\"'", `"say \"hi\""`},
		{"单引号含换行转义", "'a\\nb'", `"a\nb"`},
		{"无引号键", "{a: 1, b_c: 2, $d: [3]}", `{"a": 1, "b_c": 2, "$d": [3]}`},
		{"裸布尔", "[true, false, null]", "[true, false, null]"},
		{"前导句点", "[.5, 1.5]", "[0.5, 1.5]"},
		{"加号", "[+1, +2.5]", "[1, 2.5]"},
		{"标准 JSON 原样", `{"posts":[{"id":1,"t":"x<y>&z"}]}`, `{"posts":[{"id":1,"t":"x<y>&z"}]}`},
		{"十六进制", "[0xFF, 0x1A, 0X07]", "[255, 26, 7]"},
		{"负十六进制", "[-0x10]", "[-16]"},
		{"十六进制后逗号", "{a:0x1,b:2}", `{"a":1,"b":2}`},
		{"尾小数点", "[5., -7.]", "[5.0, -7.0]"},
		{"尾点接指数", "[5.e3]", "[5.0e3]"},
		{"双引号续行", `{"a": "line\` + "\n" + `more"}`, `{"a": "linemore"}`},
		{"双引号反斜杠x转义", `["\x41"]`, `["A"]`},
		{"单引号反斜杠x转义", `['\x41']`, `["A"]`},
		{"单引号反斜杠撇号转义", `'it\'s'`, `"it's"`},
		{"双引号反斜杠撇号", `["a\'b"]`, `["a'b"]`},
		{"Unicode键", "{ün: 1}", `{"ün": 1}`},
		{"Infinity 降级", "[Infinity, -Infinity, NaN]", "[null, null, null]"},
		{"标准 JSON 原样2", `{"a":1}`, `{"a":1}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ToJSON([]byte(tc.in))
			if err != nil {
				t.Fatalf("转换失败: %v", err)
			}
			gv := mustUnmarshal(t, got)
			wv := mustUnmarshal(t, []byte(tc.want))
			if !reflect.DeepEqual(gv, wv) {
				t.Fatalf("语义不等:\n得到 %s\n期望 %v", got, tc.want)
			}
		})
	}
}

func TestToJSONBOM(t *testing.T) {
	got, err := ToJSON([]byte("\ufeff{}"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Fatalf("BOM 应被移除: %q", got)
	}
}

func TestToJSONErrors(t *testing.T) {
	for _, in := range []string{
		"{ /* 未闭合",
		"{'a}",
		"{'a\nb'}",
		"[foo]",
		"{'a': 'b} ",
	} {
		if _, err := ToJSON([]byte(in)); err == nil {
			t.Fatalf("输入 %q 应报错", in)
		}
	}
}
