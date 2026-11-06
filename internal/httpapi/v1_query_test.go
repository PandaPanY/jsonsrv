package httpapi

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// v1DB 模仿 v1 文档示例的数据集，覆盖嵌套对象、数组字段、null、数字与布尔。
const v1DB = `{
  "posts": [
    { "id": 1, "title": "First post", "content": "about json-server", "author": { "name": "typicode", "age": 30 }, "views": 100, "tags": ["json", "server"] },
    { "id": 2, "title": "Second post", "content": "about gopher", "author": { "name": "gopher", "age": 20 }, "views": 50, "published": true, "tags": ["go"] },
    { "id": 3, "title": "Third post", "content": "about golang", "author": { "name": "gopher", "age": 25 }, "tags": ["json", "go"], "note": null }
  ],
  "comments": [
    { "id": 1, "body": "Nice comment!", "postId": 1 },
    { "id": 2, "body": "Another comment!", "postId": 2 },
    { "id": 3, "body": "Comment about second post", "postId": 2 }
  ],
  "profile": { "name": "typicode" }
}`

// idsOf 从 JSON 数组（或分页包裹的 data）中提取 id 列表。
func idsOf(t *testing.T, body string) []string {
	t.Helper()
	raw := json.RawMessage(body)
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err == nil {
		if data, ok := probe["data"]; ok {
			raw = data
		}
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("响应不是 JSON 数组: %v (%s)", err, body)
	}
	ids := make([]string, 0, len(arr))
	for _, rec := range arr {
		ids = append(ids, strings.Trim(string(rec["id"]), `"`))
	}
	return ids
}

func eqIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestV1ConditionsAndSort(t *testing.T) {
	app, _ := newTestApp(t, v1DB, Options{})
	tests := []struct {
		path string
		want []string
	}{
		{"/posts?author.name=typicode", []string{"1"}},
		{"/posts?author.name=GOPHER", nil},                        // v1 eq 严格大小写（matches-where line 47 ===）
		{"/posts?views.lt=100", []string{"1", "2", "3"}},          // 点号不是运算符分隔符：路径 views.lt 在数据里不存在，参数被忽略
		{"/posts?views.gte=50", []string{"1", "2", "3"}},          // 同上
		{"/posts?title:startsWith=first", []string{"1"}},          // v1 startsWith 两边小写后比较
		{"/posts?content:endsWith=server", []string{"1"}},         // endsWith 同样不区分大小写
		{"/posts?content:contains=gopher", []string{"2"}},         // contains 不区分大小写
		{"/posts?tags=json", []string{"1", "3"}},                  // 标量 eq 对数组字段按包含；v1 的 === 不命中已改
		{"/posts?tags:in=json,go", []string{"1", "2", "3"}},       // in 元素对数组字段同样按包含
		{"/posts?id:in=1,3", []string{"1", "3"}},                  // in 值 coerce 为数字后 === 匹配
		{"/posts?published=true", []string{"2"}},                  // "true" coerce 为布尔
		{"/posts?note=", nil},                                     // null 与空串 eq：null === "" 为 false
		{"/posts?note:ne=", []string{"3"}},                        // 缺字段的记录先被排除（field undefined→false），null !== "" 为 true
		{"/posts?title=First%20post", []string{"1"}},              // 空格与大小写
		{"/posts?q=gopher", []string{"2", "3"}},                   // 全文搜索（有意差异）
		{"/comments?q=comment", []string{"1", "2", "3"}},          // 同上
		{"/posts?author.name=gopher&views:lt=100", []string{"2"}}, // 不同参数与；3 无 views 字段被排除
		// 同名重复的普通等值按 v0.17 语义为"或"（v1 beta 的
		// setProperty 末值生效，不跟）。
		{"/posts?title=First%20post&title=Second%20post", []string{"1", "2"}},
		{"/posts?_sort=views", []string{"2", "1", "3"}},              // v1 sortOn 升序：50,100,200；缺字段记录 null 参与排序置末尾
		{"/posts?_sort=-views", []string{"3", "1", "2"}},             // 降序 200,100,50
		{"/posts?_sort=author.name,-views", []string{"3", "2", "1"}}, // 字符串按小写比较；同键按次键 -views 降序（实测）
		{"/posts?_sort=id&_order=desc", []string{"3", "2", "1"}},     // _order 按位置应用到 _sort 字段（v0.17 写法）
	}
	for _, tc := range tests {
		status, _, body := doReq(app, "GET", tc.path, "")
		if status != 200 {
			t.Fatalf("%s: 状态 %d", tc.path, status)
		}
		if got := idsOf(t, body); !eqIDs(got, tc.want) {
			t.Errorf("%s: id 顺序 %v, 期望 %v", tc.path, got, tc.want)
		}
	}
}

func TestV1Where(t *testing.T) {
	app, _ := newTestApp(t, v1DB, Options{})
	tests := []struct {
		where string
		want  []string
	}{
		// matches-where：or 的子 where 里标量值走 line 82-84 恒 false；
		// 顶层键 "author.name" 不做 dot 路径拆分 → field undefined → false。
		{`{"or":[{"id":1},{"author.name":"gopher"}]}`, nil},
		{`{"views":{"gte":50,"lt":100}}`, []string{"2"}},            // 运算符对象：3 无 views 字段被排除
		{`{"and":[{"tags":"json"},{"author.name":"gopher"}]}`, nil}, // "and" 现为保留键取全部满足；子 where 的标量值按 line 82-84 恒 false → 整体仍空
		{`{"author.age":{"gt":20}}`, nil},                           // obj["author.age"] 是 undefined（dot 非路径）
		{`{"tags":["go"]}`, nil},                                    // 数组值不是运算符对象 → field 存在仍 false（line 84）
	}
	for _, tc := range tests {
		path := "/posts?_where=" + url.QueryEscape(tc.where)
		status, _, body := doReq(app, "GET", path, "")
		if status != 200 {
			t.Fatalf("%s: 状态 %d (%s)", path, status, body)
		}
		if got := idsOf(t, body); !eqIDs(got, tc.want) {
			t.Errorf("%s: id %v, 期望 %v", path, got, tc.want)
		}
	}
	// _where 合法 JSON 时整体替换普通参数树（v1 app.js：where = parsed）；
	// 标量值按 matches-where line 82-84 恒 false → 空数组。用运算符对象形式验证覆盖效果。
	status, _, body := doReq(app, "GET", "/posts?id:in=1,3&_where="+url.QueryEscape(`{"id":{"eq":3}}`), "")
	if status != 200 || !eqIDs(idsOf(t, body), []string{"3"}) {
		t.Errorf("普通条件应被 _where 整体替换: %s", body)
	}
}

func TestV1Pages(t *testing.T) {
	app, _ := newTestApp(t, v1DB, Options{})
	// 基本包裹对象形态与链接
	status, _, body := doReq(app, "GET", "/posts?_page=2&_per_page=2", "")
	if status != 200 {
		t.Fatal(status)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"first", "prev", "next", "last", "pages", "items", "data"} {
		if _, ok := env[key]; !ok {
			t.Fatalf("分页响应缺少键 %s: %s", key, body)
		}
	}
	if string(env["pages"]) != "2" || string(env["items"]) != "3" {
		t.Fatalf("pages/items 不符: %s", body)
	}
	if !eqIDs(idsOf(t, body), []string{"3"}) {
		t.Errorf("data 不符: %s", body)
	}
	// 对照 1.0.0-beta.15 实测：first/prev/next/last 是页号而不是 URL。
	if string(env["first"]) != "1" {
		t.Errorf("first 应为页号 1: %s", env["first"])
	}
	if string(env["prev"]) != "1" {
		t.Errorf("prev 应为页号 1: %s", env["prev"])
	}
	if string(env["last"]) != "2" {
		t.Errorf("last 应为页号 2: %s", env["last"])
	}
	if next := string(env["next"]); next != "null" {
		t.Errorf("最后一页 next 应为 null: %s", next)
	}

	// 非法与越界页码自动修正
	if _, _, b := doReq(app, "GET", "/posts?_page=0", ""); !eqIDs(idsOf(t, b), []string{"1", "2", "3"}) {
		t.Errorf("_page=0 应修正为第 1 页: %s", b)
	}
	if _, _, b := doReq(app, "GET", "/posts?_page=abc", ""); !eqIDs(idsOf(t, b), []string{"1", "2", "3"}) {
		t.Errorf("非法 _page 应修正: %s", b)
	}
	// 默认 _per_page=10、共 3 条 → 只有 1 页，越界页码修正到唯一一页（全部记录）
	if _, _, b := doReq(app, "GET", "/posts?_page=99", ""); !eqIDs(idsOf(t, b), []string{"1", "2", "3"}) {
		t.Errorf("_page 越界应修正到最后一页: %s", b)
	}
}

func TestV1Embeds(t *testing.T) {
	app, _ := newTestApp(t, v1DB, Options{})
	// 集合上的子嵌入
	_, _, body := doReq(app, "GET", "/posts?_embed=comments", "")
	var posts []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &posts); err != nil {
		t.Fatal(err)
	}
	if !eqIDs(idsOf(t, string(posts[0]["comments"])), []string{"1"}) {
		t.Errorf("post1.comments 不符: %s", posts[0]["comments"])
	}
	if !eqIDs(idsOf(t, string(posts[1]["comments"])), []string{"2", "3"}) {
		t.Errorf("post2.comments 不符")
	}
	if got := idsOf(t, string(posts[2]["comments"])); len(got) != 0 {
		t.Errorf("post3.comments 应为空: %v", got)
	}
	// 单条上的子嵌入
	_, _, body = doReq(app, "GET", "/posts/1?_embed=comments", "")
	var post map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &post); err != nil {
		t.Fatal(err)
	}
	if !eqIDs(idsOf(t, string(post["comments"])), []string{"1"}) {
		t.Errorf("单条 _embed 不符: %s", body)
	}
	// 父嵌入（v1 语法：单数关系名）
	_, _, body = doReq(app, "GET", "/comments?_embed=post", "")
	var comments []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &comments); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(comments[0]["post"]), `"id":1`) {
		t.Errorf("comment1.post 应为 post1: %s", comments[0]["post"])
	}
	if !strings.Contains(string(comments[1]["post"]), `"id":2`) {
		t.Errorf("comment2.post 应为 post2: %s", comments[1]["post"])
	}
}
