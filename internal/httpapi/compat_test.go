package httpapi

// 表驱动兼容测试：数据与对照脚本共用 scripts/compare-db.json，
// 用例覆盖 json-server 两个版本 README 里的全部示例。
// 断言范围：列表断言 id 序列、单条断言完整 JSON、
// 分页/切片断言响应头与包裹对象字段。

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// loadCompareDB 读取对照示例库 scripts/compare-db.json。
func loadCompareDB(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash("../../scripts/compare-db.json"))
	if err != nil {
		t.Fatalf("读取示例库: %v", err)
	}
	return string(data)
}

// docCase 是表驱动条目；want 用前缀语法：
//
//	@j:<紧凑 JSON>                响应体逐字节等价
//	@ids:<csv>                    数组响应体的 id 序列
//	@env:<first,prev,next,last,pages,items>|<ids csv>   v1 分页包裹对象（null 记为 -）
//	@slice:<X-Total-Count>|<ids csv>                    切片响应头 + id 序列
//	@link:<Link 原文>             Link 头精确匹配（空串表示无该头）
//	@ids2:<csv>                   包裹对象 data 的 id 序列（配合 @env）
//	@html:                        HTML 首页
//	@contains:<文本>              包含子串
//
// 多个断言用 || 连接。
type docCase struct {
	method     string
	path       string
	body       string
	wantStatus int
	want       string
}

// csvRange 生成 from..to 的逗号序列。
func csvRange(from, to int) string {
	out := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return strings.Join(out, ",")
}

// postJ 返回第 i 条 posts 记录的紧凑 JSON（compare-db 形状）。
func postJ(i int) string {
	author := "alice"
	tags := `["a"]`
	if i%2 == 0 {
		author = "bob"
		tags = `["b","c"]`
	}
	return `{"id":` + strconv.Itoa(i) + `,"title":"Post ` + strconv.Itoa(i) +
		`","views":` + strconv.Itoa(i*10) + `,"author":{"name":"` + author + `"},"tags":` + tags + `}`
}

func checkWant(t *testing.T, body string, heads http.Header, want string) {
	t.Helper()
	for _, spec := range strings.Split(want, "||") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		switch {
		case strings.HasPrefix(spec, "@j:"):
			got := compactOrBody(t, body)
			if exp := spec[3:]; got != exp {
				t.Errorf("响应体不符:\n got: %s\nwant: %s", got, exp)
			}
		case strings.HasPrefix(spec, "@ids:"):
			if got := idsOf(t, body); !eqIDs(got, splitCSV(spec[5:])) {
				t.Errorf("id 序列不符: got=%v want=%v body=%s", got, splitCSV(spec[5:]), firstN(body, 200))
			}
		case strings.HasPrefix(spec, "@env:"):
			checkEnvelope(t, body, spec[5:])
		case strings.HasPrefix(spec, "@slice:"):
			total, ids, _ := strings.Cut(spec[7:], "|")
			if got := heads.Get("X-Total-Count"); got != total {
				t.Errorf("X-Total-Count=%q 期望 %q", got, total)
			}
			if got := idsOf(t, body); !eqIDs(got, splitCSV(ids)) {
				t.Errorf("切片 id 不符: got=%v", got)
			}
		case strings.HasPrefix(spec, "@link:"):
			exp := spec[6:]
			if got := heads.Get("Link"); got != exp {
				t.Errorf("Link 头不符:\n got: %q\nwant: %q", got, exp)
			}
		case strings.HasPrefix(spec, "@html:"):
			if !strings.Contains(strings.ToLower(body), "doctype") {
				t.Errorf("应为 HTML 首页: %s", firstN(body, 120))
			}
		case strings.HasPrefix(spec, "@contains:"):
			if !strings.Contains(body, spec[10:]) {
				t.Errorf("响应体应包含 %q: %s", spec[10:], firstN(body, 200))
			}
		case strings.HasPrefix(spec, "@notcontains:"):
			if strings.Contains(body, spec[13:]) {
				t.Errorf("响应体不应包含 %q: %s", spec[13:], firstN(body, 200))
			}
		default:
			t.Fatalf("want 语法未知: %q", spec)
		}
	}
}

// compactOrBody 把 body 解析为 JSON 再紧凑序列化（保持键顺序）；解析失败返回原文。
func compactOrBody(t *testing.T, body string) string {
	t.Helper()
	v, err := jsonx.Parse([]byte(body))
	if err != nil {
		return body
	}
	out, err := jsonx.MarshalCompact(v)
	if err != nil {
		return body
	}
	return string(out)
}

// checkEnvelope 断言 v1 包裹对象的页号字段与 data 的 id 序列。
// 格式：first,prev,next,last,pages,items|ids（null 记为 -）
func checkEnvelope(t *testing.T, body string, spec string) {
	t.Helper()
	fields, ids, _ := strings.Cut(spec, "|")
	want := splitCSV(fields)
	v, err := jsonx.Parse([]byte(body))
	if err != nil {
		t.Fatalf("包裹对象解析: %v %s", err, firstN(body, 160))
	}
	o, ok := jsonx.AsObject(v)
	if !ok {
		t.Fatalf("应为包裹对象: %s", firstN(body, 160))
	}
	names := []string{"first", "prev", "next", "last", "pages", "items"}
	if len(want) != len(names) {
		t.Fatalf("@env 需要 6 个字段: %q", spec)
	}
	for i, name := range names {
		val, _ := o.Get(name)
		got := "null"
		if val != nil {
			got, _ = jsonx.ToString(val)
		}
		if want[i] == "-" {
			want[i] = "null"
		}
		if got != want[i] {
			t.Errorf("包裹对象 %s=%s 期望 %s（body=%s）", name, got, want[i], firstN(body, 200))
		}
	}
	if ids != "" {
		data, _ := o.Get("data")
		raw, _ := jsonx.MarshalCompact(data)
		if got := idsOf(t, string(raw)); !eqIDs(got, splitCSV(ids)) {
			t.Errorf("data id 序列不符: got=%v want=%v", got, splitCSV(ids))
		}
	}
}

func runDocCases(t *testing.T, app *App, cases []docCase) {
	t.Helper()
	for _, tc := range cases {
		status, heads, body := doReq(app, tc.method, tc.path, tc.body)
		if status != tc.wantStatus {
			t.Errorf("%s %s: 状态 %d, 期望 %d（%s）", tc.method, tc.path, status, tc.wantStatus, firstN(body, 160))
			continue
		}
		if tc.want != "" {
			checkWant(t, body, heads, tc.want)
		}
	}
}

func TestDocExamplesV1(t *testing.T) {
	app, _ := newTestApp(t, loadCompareDB(t), Options{})

	// 读取组：单资源、查询、排序、分页、嵌入。
	runDocCases(t, app, []docCase{
		{"GET", "/posts", "", 200, "@ids:" + csvRange(1, 25)},
		{"GET", "/posts/1", "", 200, "@j:" + postJ(1)},
		{"GET", "/posts/2", "", 200, "@j:" + postJ(2)},
		{"GET", "/posts/999", "", 404, `@j:{"message":"record not found"}`},
		{"GET", "/posts?_embed=comments", "", 200, `@contains:"comments":[{"id":3,"body":"c3","postId":1},{"id":6,"body":"c6","postId":1},{"id":9,"body":"c9","postId":1}]`},
		{"GET", "/comments/3?_embed=post", "", 200, `@j:{"id":3,"body":"c3","postId":1,"post":{"id":1,"title":"Post 1","views":10,"author":{"name":"alice"},"tags":["a"]}}`},
		// v1 embed()：singularize(nope)==nope 且 db 无对应复数键 → 原样返回（无该键）。
		// 0.17 的 "nope": [] 行为仅属 legacy 组。依据 v1 service.js 和实测。
		{"GET", "/posts/1?_embed=nope", "", 200, "@j:" + postJ(1)},
		{"GET", "/posts?author.name:eq=alice", "", 200, "@ids:1,3,5,7,9,11,13,15,17,19,21,23,25"},
		{"GET", "/posts?title:contains=post", "", 200, "@ids:" + csvRange(1, 25)},
		{"GET", "/posts?title:startsWith=Post%201", "", 200, "@ids:1,10,11,12,13,14,15,16,17,18,19"},
		{"GET", "/posts?title:endsWith=%201", "", 200, "@ids:1,11,21"},
		{"GET", "/posts?views:gt=200", "", 200, "@ids:21,22,23,24,25"},
		{"GET", "/posts?views:gte=200", "", 200, "@ids:20,21,22,23,24,25"},
		{"GET", "/posts?views:lt=30", "", 200, "@ids:1,2"},
		{"GET", "/posts?id:in=2,3,4", "", 200, "@ids:2,3,4"},
		{"GET", "/posts?id:ne=1", "", 200, "@ids:" + strings.TrimPrefix(csvRange(2, 25), "")},
		{"GET", "/posts?title:eq=Post%202", "", 200, "@j:[" + postJ(2) + "]"},
		{"GET", "/mixed?s:eq=apple", "", 200, "@ids:1,4"},
		{"GET", "/mixed?s:eq=APPLE", "", 200, "@j:[]"},
		{"GET", "/mixed?v:ne=10", "", 200, "@ids:2,4"},
		{"GET", "/posts?views:contains=25", "", 200, "@j:[]"},
		{"GET", "/mixed?s:startsWith=app", "", 200, "@ids:1,4"},
		{"GET", "/mixed?s:endsWith=pple", "", 200, "@ids:1,4"},
		{"GET", "/posts?_sort=title", "", 200, "@ids:1,10,11,12,13,14,15,16,17,18,19,2,20,21,22,23,24,25,3,4,5,6,7,8,9"},
		{"GET", "/posts?_sort=-views", "", 200, "@ids:" + strings.Join(reverseRange(25, 1), ",")},
		{"GET", "/posts?_sort=author.name,-views", "", 200, "@ids:25,23,21,19,17,15,13,11,9,7,5,3,1,24,22,20,18,16,14,12,10,8,6,4,2"},
		{"GET", "/mixed?_sort=v", "", 200, "@ids:4,1,3,2"},
		{"GET", "/mixed?_sort=s", "", 200, "@ids:1,4,2,3"},
		{"GET", "/posts?q=Post", "", 200, "@ids:" + csvRange(1, 25)},
		{"GET", "/posts?_page=2&_per_page=10", "", 200, "@env:1,1,3,3,3,25|" + csvRange(11, 20)},
		{"GET", "/posts?_page=1&_per_page=3", "", 200, "@env:1,-,2,9,9,25|1,2,3"},
		{"GET", "/posts?_page=9&_per_page=10", "", 200, "@env:1,2,-,3,3,25|" + csvRange(21, 25)},
		// or:（views gt 200 → 21..25）∪（author.name lt "b" → 全部 alice 奇数 id）
		{"GET", `/posts?_where={"or":[{"views":{"gt":200}},{"author":{"name":{"lt":"b"}}}]}`, "", 200,
			"@ids:1,3,5,7,9,11,13,15,17,19,21,22,23,24,25"},
		{"GET", `/posts?_where={"author":{"name":{"eq":"bob"}}}&_page=1&_per_page=3`, "", 200, "@env:1,-,2,4,4,12|2,4,6"},
		{"GET", `/posts?_where={"author":{"name":{"lt":"b"}}}&_page=1&_per_page=3`, "", 200, "@env:1,-,2,5,5,13|1,3,5"},
		{"GET", `/posts?_where={"author.name":{"eq":"bob"}}}&_page=1&_per_page=3`, "", 200, "@env:1,-,-,1,1,0|"},
		{"GET", "/articles?_where=notjson", "", 200, "@ids:1,2"},
		{"GET", "/articles/1?_embed=notes", "", 200, `@j:{"id":1,"title":"art one","notes":[{"id":1,"body":"n1","articleId":1}]}`},
		{"GET", "/movies/1?_embed=reviews", "", 200, `@j:{"id":1,"name":"m1","reviews":[{"id":1,"text":"r1","movieId":1}]}`},
		// 0.17/v1 实测：/images/1 的 comments
		// 外键应为 imageId，comments 集合无一条指向 images，两模式均为 "comments": []，两个键都在。
		{"GET", "/images/1?_embed=thumbs&_embed=comments", "", 200,
			`@j:{"id":1,"url":"u1","thumbs":[{"id":1,"imageId":1}],"comments":[]}`},
		{"GET", "/notes/1?_embed=article", "", 200, `@j:{"id":1,"body":"n1","articleId":1,"article":{"id":1,"title":"art one"}}`},
		{"GET", "/people/1?_embed=address", "", 200, `@j:{"id":1,"addressId":1,"name":"Pat","address":{"id":1,"city":"x"}}`},
		{"GET", "/articles/1/notes", "", 200, "@ids:1"},
		{"GET", "/movies/1/reviews", "", 200, "@ids:1"},
		{"GET", "/images/1/thumbs", "", 200, "@ids:1"},
		{"GET", "/db", "", 200, `@contains:"posts":[`},
		{"GET", "/", "", 200, "@html:"},
	})

	// 写组：POST、PUT、PATCH、DELETE。
	runDocCases(t, app, []docCase{
		{"POST", "/posts", `{"title":"new post","views":999}`, 201, `@contains:"title":"new post"||@contains:"views":999`},
		{"POST", "/posts", `{"id":2,"title":"dup id"}`, 409, `@j:{"message":"id \"2\" already exists"}`},
		{"PUT", "/posts/2", `{"title":"put replaced"}`, 200, `@j:{"id":2,"title":"put replaced"}`},
		{"PATCH", "/posts/3", `{"views":42}`, 200, `@j:{"id":3,"title":"Post 3","views":42,"author":{"name":"alice"},"tags":["a"]}`},
		{"PUT", "/profile", `{"name":"putter"}`, 200, `@j:{"name":"putter"}`},
		{"PATCH", "/profile", `{"name":"patcher"}`, 200, `@j:{"name":"patcher"}`},
		{"DELETE", "/posts/4?_dependent=comments", "", 200, "@j:" + postJ(4)},
		{"GET", "/posts/4", "", 404, ""},
		// 默认模式：嵌套 POST 写 articleId 而非 articlId；
		// _dependent 级联删除后 notes 中 articleId=1 的记录（含新写入的）都不在。
		{"POST", "/articles/1/notes", `{"b":1}`, 201, `@contains:"articleId":1`},
		{"POST", "/articles/1/notes", `{"b":1}`, 201, `@notcontains:"articlId"`},
		{"DELETE", "/articles/1?_dependent=notes", "", 200, `@contains:"title":"art one"`},
		{"GET", "/notes", "", 200, "@ids:2"},
	})
	// 写后状态：posts 少了 id 4。
	_, _, body := doReq(app, "GET", "/posts", "")
	want := append(splitCSV(csvRange(1, 3)), splitCSV("5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25")...)
	if got := idsOf(t, body); len(got) != 25 {
		t.Errorf("写后 posts 应有 25 条: got=%v want=%v...", got, want)
	}
}

// reverseRange 生成 from..to 逆序列表（from 大于 to）。
func reverseRange(from, to int) []string {
	out := make([]string, 0, from-to+1)
	for i := from; i >= to; i-- {
		out = append(out, strconv.Itoa(i))
	}
	return out
}

// TestDocExamplesV017 对照 0.17.4 的 --legacy 行为。
func TestDocExamplesV017(t *testing.T) {
	app, _ := newTestApp(t, loadCompareDB(t), Options{Legacy: true})

	runDocCases(t, app, []docCase{
		{"GET", "/", "", 200, "@html:"},
		{"GET", "/posts", "", 200, "@ids:" + csvRange(1, 25)},
		{"GET", "/posts/1", "", 200, "@j:" + postJ(1)},
		{"GET", "/db", "", 200, `@contains:"comments":[`},
		{"GET", "/posts/1/comments", "", 200, "@ids:3,6,9"},
		// 0.17 实测：X-Total-Count 是筛选后、切片前的总数。
		{"GET", "/posts/1/comments?_start=0&_limit=2", "", 200, "@slice:3|3,6"},
		{"GET", "/articles/1/notes", "", 200, "@ids:1"},
		{"GET", "/movies/1/reviews", "", 200, "@ids:1"},
		{"GET", "/images/1/thumbs", "", 200, "@ids:1"},
		{"GET", "/posts/999/comments", "", 200, "@j:[]"},
		{"GET", "/articles/1?_embed=notes", "", 200, `@j:{"id":1,"title":"art one","notes":[{"id":1,"body":"n1","articleId":1}]}`},
		{"GET", "/movies/1?_embed=reviews", "", 200, `@j:{"id":1,"name":"m1","reviews":[{"id":1,"text":"r1","movieId":1}]}`},
		// 同名 _embed 重复全部生效；0.17 实测 comments 亦为 []（同上）。
		{"GET", "/images/1?_embed=thumbs&_embed=comments", "", 200,
			`@j:{"id":1,"url":"u1","thumbs":[{"id":1,"imageId":1}],"comments":[]}`},
		{"GET", "/articles/1?_embed=nope", "", 200, `@j:{"id":1,"title":"art one","nope":[]}`},
		{"GET", "/notes/1?_expand=article", "", 200, `@j:{"id":1,"body":"n1","articleId":1,"article":{"id":1,"title":"art one"}}`},
		{"GET", "/people/1?_expand=address", "", 200, `@j:{"id":1,"addressId":1,"name":"Pat","address":{"id":1,"city":"x"}}`},
		// 0.17 实测：legacy 下 _embed 只解析"目标集合=参数名"的子嵌入，
		// 集合不存在时写空数组（post → []）；取父记录需用 _expand。
		{"GET", "/comments/1?_embed=post", "", 200, `@j:{"id":1,"body":"c1","postId":2,"post":[]}`},
		{"GET", "/posts?id=2&id=3", "", 200, "@ids:2,3"},
		{"GET", "/posts?id_ne=1", "", 200, "@ids:" + csvRange(2, 25)},
		{"GET", "/posts?views_gte=200", "", 200, "@ids:" + csvRange(20, 25)},
		{"GET", "/posts?views_lte=20", "", 200, "@ids:1,2"},
		// 0.17 实测：正则 /Post[12]/i 不匹配 "Post N"（带空格）→ 空结果。
		{"GET", "/posts?title_like=Post[12]", "", 200, "@j:[]"},
		{"GET", "/mixed?v_lte=5", "", 200, "@ids:4"},
		{"GET", "/mixed?s=APPLE", "", 200, "@j:[]"},
		{"GET", "/posts?q=250", "", 200, "@ids:25"},
		{"GET", "/posts?_sort=views&_order=desc", "", 200, "@ids:" + strings.Join(reverseRange(25, 1), ",")},
		{"GET", "/posts?_sort=title,views&_order=desc,asc", "", 200,
			"@ids:9,8,7,6,5,4,3,25,24,23,22,21,20,2,19,18,17,16,15,14,13,12,11,10,1"},
		{"GET", "/mixed?_sort=v", "", 200, "@ids:4,1,2,3"},
		{"GET", "/mixed?_sort=s", "", 200, "@ids:2,1,4,3"},
		{"GET", "/posts?_start=0&_end=2", "", 200, "@slice:25|1,2"},
		{"GET", "/posts?_start=20&_end=30", "", 200, "@slice:25|" + csvRange(21, 25)},
		{"GET", "/posts?_limit=3", "", 200, "@slice:25|1,2,3"},
		// 0.17 实测：Link = "http://Host + 原始查询串" 原地替换页号（不重排参数、
		// 不加缺失参数）；越界页返回 [] 且 Link 为空。
		{"GET", "/posts?_page=2&_limit=10", "", 200,
			`@link:<http://example.com/posts?_page=1&_limit=10>; rel="first", <http://example.com/posts?_page=1&_limit=10>; rel="prev", <http://example.com/posts?_page=3&_limit=10>; rel="next", <http://example.com/posts?_page=3&_limit=10>; rel="last"||@ids:` + csvRange(11, 20)},
		{"GET", "/posts?_page=99&_limit=5", "", 200, "@j:[]||@link:"},
	})

	// 写组（含自动级联）。
	runDocCases(t, app, []docCase{
		{"DELETE", "/posts/1", "", 200, "@j:{}"},
		{"GET", "/comments", "", 200, "@ids:1,2,4,5,7,8"},
		{"POST", "/posts", `{"title":"new post"}`, 201, `@j:{"title":"new post","id":26}`},
		{"POST", "/profile", `{"name":"posted"}`, 201, `@contains:posted`},
		{"PATCH", "/posts/2", `{"views":42}`, 200, `@j:{"id":2,"title":"Post 2","views":42,"author":{"name":"bob"},"tags":["b","c"]}`},
		// 0.17 destroy 全局孤儿清理（mixins.getRemovable）：DELETE /posts/1 已级联删掉
		// comments（见上行），再删同一 id 只能 404。这里改删 /posts/2 验证 _dependent
		// 参数路径同样级联（0.17 实测），断言剩余 comments。
		{"DELETE", "/posts/2?_dependent=comments", "", 200, "@j:{}"},
		{"GET", "/comments", "", 200, "@ids:2,5,8"},
		// legacy：嵌套 POST 写 articleId（0.17 实测外键值为字符串）；
		// _dependent 级联删除后 notes 中 articleId=1 的记录都不在。
		// 0.17 实测：嵌套 POST 响应含 insert 生成的 id。
		{"POST", "/articles/1/notes", `{"b":1}`, 201, `@j:{"b":1,"articleId":"1","id":3}`},
		{"DELETE", "/articles/1?_dependent=notes", "", 200, "@j:{}"},
		{"GET", "/notes", "", 200, "@ids:2"},
	})

	// 表单与 text/plain 请求体（需要自定义 Content-Type）。
	status, _, body := doReqH(app, "POST", "/articles", "title=formpost",
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if status != 201 || !strings.Contains(body, "formpost") {
		t.Errorf("表单 POST: %d %s", status, firstN(body, 160))
	}
	status, _, body = doReqH(app, "POST", "/articles", `{"title":"ignored"}`,
		map[string]string{"Content-Type": "text/plain"})
	if status != 201 || compactOrBody(t, body) != `{"id":4}` {
		t.Errorf("text/plain POST 应忽略请求体只生成 id: %d %s", status, firstN(body, 160))
	}
}

// TestHeadRequests 断言所有 GET 路由支持 HEAD：
// 状态码与 GET 相同、响应体为空、响应头一致。
func TestHeadRequests(t *testing.T) {
	app, _ := newTestApp(t, loadCompareDB(t), Options{})
	for _, p := range []string{"/posts", "/posts/1", "/profile", "/posts/1/comments", "/db", "/"} {
		status, _, body := doReq(app, "HEAD", p, "")
		if status != 200 {
			t.Errorf("HEAD %s: 状态 %d", p, status)
		}
		if body != "" {
			t.Errorf("HEAD %s 应为空体: %q", p, firstN(body, 80))
		}
	}
	app2, _ := newTestApp(t, loadCompareDB(t), Options{Legacy: true})
	if status, heads, _ := doReq(app2, "HEAD", "/posts?_limit=3", ""); status != 200 || heads.Get("X-Total-Count") != "25" {
		t.Errorf("HEAD 切片响应头: %d %q", status, heads.Get("X-Total-Count"))
	}
	if status, heads, _ := doReq(app2, "HEAD", "/posts?_page=2&_limit=10", ""); status != 200 || heads.Get("Link") == "" {
		t.Errorf("HEAD legacy 分页响应头: %d", status)
	}
}
