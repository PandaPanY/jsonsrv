package httpapi

// 参数边界情况：嵌套路由的特殊 id、非对象父字段、未知参数、q、切片解析。

import (
	"encoding/json"
	"strings"
	"testing"
)

const edgeCaseDB = `{
  "orders": [
    {"id": "007", "dims": null, "memo": "A"},
    {"id": "12e4", "dims": {"w": 0, "x": {"y": 2}}},
    {"id": "0x10", "dims": 5},
    {"id": "true", "dims": {"w": 1}},
    {"id": "null"}
  ],
  "lines": [
    {"id": 1, "orderId": "007"},
    {"id": 2, "orderId": 7},
    {"id": 3, "orderId": "12e4"}
  ],
  "nums": [{"id": 7}],
  "kids": [{"id": 1, "numId": 7}, {"id": 2, "numId": "7"}, {"id": 3, "numId": 8}],
  "a": [
    {"id": 1, "s": "x", "p": 1.50, "n": 1000, "on": true},
    {"id": 2, "s": "y", "p": 2, "n": 5, "on": false}
  ]
}`

func edgeCaseIDs(t *testing.T, app *App, path string) string {
	t.Helper()
	status, _, body := doReq(app, "GET", path, "")
	if status != 200 {
		t.Fatalf("GET %s：状态 %d (%s)", path, status, body)
	}
	return strings.Join(idsOf(t, body), ",")
}

func TestNestedPostKeepsParentID(t *testing.T) {
	app, _ := newTestApp(t, edgeCaseDB, Options{})
	cases := []struct {
		path, fk string
		want     any
	}{
		{"/orders/007/lines", "orderId", "007"},
		{"/orders/12e4/lines", "orderId", "12e4"},
		{"/orders/0x10/lines", "orderId", "0x10"},
		{"/orders/true/lines", "orderId", "true"},
		{"/orders/null/lines", "orderId", "null"},
		{"/orders/nope/lines", "orderId", "nope"}, // 父不存在：路径原文
		{"/nums/7/kids", "numId", float64(7)},     // 数字父 id 保持数字
		{"/nums/999/kids", "numId", "999"},        // 父不存在：路径原文，不猜类型
	}
	for _, c := range cases {
		status, _, body := doReq(app, "POST", c.path, `{"z":1}`)
		if status != 201 {
			t.Fatalf("POST %s：状态 %d (%s)", c.path, status, body)
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(body), &rec); err != nil {
			t.Fatal(err)
		}
		if rec[c.fk] != c.want {
			t.Errorf("POST %s：%s = %#v，期望 %#v", c.path, c.fk, rec[c.fk], c.want)
		}
	}
	// 写入后能通过 _embed 查回
	_, _, body := doReq(app, "GET", "/orders/12e4?_embed=lines", "")
	var order struct {
		Lines []map[string]any `json:"lines"`
	}
	if err := json.Unmarshal([]byte(body), &order); err != nil {
		t.Fatal(err)
	}
	if len(order.Lines) != 2 {
		t.Errorf("/orders/12e4?_embed=lines：%d 条，期望 2 条（原有 1 条 + 新写入 1 条）: %s", len(order.Lines), body)
	}
	// 请求体显式给的外键不被覆盖（默认模式）
	_, _, body = doReq(app, "POST", "/orders/007/lines", `{"orderId":"mine"}`)
	if !strings.Contains(body, `"orderId":"mine"`) {
		t.Errorf("显式外键被覆盖: %s", body)
	}
}

func TestNestedGetDoesNotCoercePathID(t *testing.T) {
	app, _ := newTestApp(t, edgeCaseDB, Options{})
	if got := edgeCaseIDs(t, app, "/orders/007/lines"); got != "1" {
		t.Errorf("/orders/007/lines：%s，期望 1（不能匹配数字 7）", got)
	}
	// 数字路径段仍能同时匹配数字和字符串外键
	if got := edgeCaseIDs(t, app, "/nums/7/kids"); got != "1,2" {
		t.Errorf("/nums/7/kids：%s，期望 1,2", got)
	}
}

func TestNonObjectParentDoesNotMatch(t *testing.T) {
	app, _ := newTestApp(t, edgeCaseDB, Options{})
	cases := []struct{ path, want string }{
		{"/orders?dims.w=0", "12e4"},
		{"/orders?dims.w:eq=0", "12e4"},
		{"/orders?dims.w:ne=0", "true"},
		{"/orders?dims.w_ne=0", "true"},
		{"/orders?dims.w_gte=0", "12e4,true"},
		{"/orders?dims.w:lt=1", "12e4"},
		{"/orders?dims.w:in=0,1", "12e4,true"},
		{"/orders?dims.x.y=2", "12e4"},
		{"/orders?dims.x.y_ne=2", ""},
		{`/orders?_where={"dims":{"w":{"eq":0}}}`, "12e4"},
		{`/orders?_where={"dims":{"x":{"y":{"gt":0}}}}`, "12e4"},
	}
	for _, c := range cases {
		if got := edgeCaseIDs(t, app, c.path); got != c.want {
			t.Errorf("%s：[%s]，期望 [%s]", c.path, got, c.want)
		}
	}
}

func TestUnknownParamIgnored(t *testing.T) {
	app, _ := newTestApp(t, edgeCaseDB, Options{})
	cases := []struct{ path, want string }{
		{"/a?_=1699", "1,2"},             // jQuery 防缓存
		{"/a?_t=1699", "1,2"},            // 常见防缓存写法
		{"/a?t=1699", "1,2"},             // 同上
		{"/a?foo=bar", "1,2"},            // 未知字段
		{"/a?foo=1&foo=2", "1,2"},        // 同名重复
		{"/a?x.y=1", "1,2"},              // 未知的嵌套路径
		{"/a?callback=", "1,2"},          // 空值
		{"/a?s=x&foo=bar", "1"},          // 其他条件照常生效
		{"/a?foo=bar&_limit=1", "1"},     // 切片照常生效
		{"/orders/007/lines?foo=b", "1"}, // 嵌套路由
		{"/a?s=nope", ""},                // 字段存在、值不匹配：仍然是空
		{"/a?foo:eq=1", ""},              // 显式 v1 运算符：字段不存在不匹配
		{"/a?foo:ne=1", ""},              // 同上（缺失字段不匹配）
	}
	for _, c := range cases {
		if got := edgeCaseIDs(t, app, c.path); got != c.want {
			t.Errorf("%s：[%s]，期望 [%s]", c.path, got, c.want)
		}
	}
	// --legacy 本来就是这样，确认没有被破坏
	legacy, _ := newTestApp(t, edgeCaseDB, Options{Legacy: true})
	if got := edgeCaseIDs(t, legacy, "/a?foo=bar"); got != "1,2" {
		t.Errorf("--legacy /a?foo=bar：[%s]", got)
	}
}

func TestQMatchesNonStringFields(t *testing.T) {
	app, _ := newTestApp(t, edgeCaseDB, Options{})
	cases := []struct{ path, want string }{
		{"/a?q=1000", "1"},
		{"/a?q=true", "1"},
		{"/a?q=X", "1"},
		{"/a?q=", "1,2"},
	}
	for _, c := range cases {
		if got := edgeCaseIDs(t, app, c.path); got != c.want {
			t.Errorf("%s：[%s]，期望 [%s]", c.path, got, c.want)
		}
	}
}

func TestLegacyNumberComparedAsJSString(t *testing.T) {
	legacy, _ := newTestApp(t, edgeCaseDB, Options{Legacy: true})
	if got := edgeCaseIDs(t, legacy, "/a?p=1.5"); got != "1" {
		t.Errorf("--legacy p=1.5：[%s]，期望 1（文件里写的是 1.50）", got)
	}
	if got := edgeCaseIDs(t, legacy, "/a?p=1.50"); got != "" {
		t.Errorf("--legacy p=1.50：[%s]，期望空（0.17 比较的是 String(1.5)）", got)
	}
	// 响应里的数字字面量仍然保留原文
	_, _, body := doReq(legacy, "GET", "/a/1", "")
	if !strings.Contains(body, `"p":1.50`) {
		t.Errorf("数字字面量应保留原文: %s", body)
	}
}

func TestLimitIsDecimalOnly(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		app, _ := newTestApp(t, edgeCaseDB, Options{Legacy: legacy})
		if got := edgeCaseIDs(t, app, "/a?_limit=0x2"); got != "" {
			t.Errorf("legacy=%v _limit=0x2：[%s]，期望空（parseInt 十进制得 0）", legacy, got)
		}
	}
}

// 默认模式沿用 v1 的宽松比较，README「已知限制」写明了这些行为；这里固定下来，改动时要同步文档。
func TestLooseMatchesDocumented(t *testing.T) {
	const db = `{"t":[{"id":1,"zip":2134,"n":5,"big":1000,"w":null},{"id":2,"zip":"02134","n":6,"big":2,"w":3}]}`
	app, _ := newTestApp(t, db, Options{})
	cases := []struct{ path, want string }{
		{"/t?zip=02134", "1,2"},
		{"/t?n=05", "1"},
		{"/t?big=1e3", "1"},
		{"/t?w_gte=0", "1,2"},
	}
	for _, c := range cases {
		if got := edgeCaseIDs(t, app, c.path); got != c.want {
			t.Errorf("%s：[%s]，期望 [%s]", c.path, got, c.want)
		}
	}
	legacy, _ := newTestApp(t, db, Options{Legacy: true})
	if got := edgeCaseIDs(t, legacy, "/t?zip=02134"); got != "2" {
		t.Errorf("--legacy zip=02134：[%s]，期望 2", got)
	}
}
