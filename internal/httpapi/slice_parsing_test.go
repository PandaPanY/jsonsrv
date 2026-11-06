package httpapi

// _start/_end/_limit 按 JS parseInt 语义解析，空值视为没有传参数。
// 两种模式行为相同。全部 6 条 = products 的 1..6。

import "testing"

// 空参数不算传；1.5→1、2abc→2、abc→NaN、-1 按 _.slice 尾部裁剪。
var sliceParseCases = []struct {
	path string
	ids  []string
	xtc  string
}{
	{"/products?_limit=", []string{"1", "2", "3", "4", "5", "6"}, ""},
	{"/products?_end=", []string{"1", "2", "3", "4", "5", "6"}, ""},
	{"/products?_limit=-1", []string{"1", "2", "3", "4", "5"}, "6"},
	{"/products?_limit=1.5", []string{"1"}, "6"},
	{"/products?_limit=2abc", []string{"1", "2"}, "6"},
	{"/products?_limit=abc", nil, "6"},
	{"/products?_start=1.5&_limit=1", []string{"2"}, "6"},
}

func runT3(t *testing.T, opts Options) {
	app, _ := newTestApp(t, stringMatchDB, opts)
	for _, tc := range sliceParseCases {
		status, head, body := doReq(app, "GET", tc.path, "")
		if status != 200 {
			t.Fatalf("%s: 状态 %d (%s)", tc.path, status, body)
		}
		got := idsOf(t, body)
		want := tc.ids
		if len(got) != len(want) {
			t.Errorf("%s: id %v, 期望 %v", tc.path, got, want)
		} else {
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("%s: id %v, 期望 %v", tc.path, got, want)
					break
				}
			}
		}
		if xtc := head.Get("X-Total-Count"); xtc != tc.xtc {
			t.Errorf("%s: X-Total-Count = %q, 期望 %q", tc.path, xtc, tc.xtc)
		}
	}
}

func TestSliceJSIntParsingDefault(t *testing.T) { runT3(t, Options{}) }
func TestSliceJSIntParsingLegacy(t *testing.T)  { runT3(t, Options{Legacy: true}) }
