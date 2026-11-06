package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PandaPanY/jsonsrv/internal/routes"
)

// legacyDB 提供 3 条 posts、3 条 comments，供 v0.17 兼容行为测试。
const legacyDB = `{
  "posts": [
    { "id": 1, "title": "First post", "views": 100 },
    { "id": 2, "title": "Second post", "views": 50 },
    { "id": 3, "title": "Third post", "views": 200 }
  ],
  "comments": [
    { "id": 1, "body": "Nice comment", "postId": 1 },
    { "id": 2, "body": "Another comment", "postId": 2 },
    { "id": 3, "body": "Comment about second post", "postId": 2 }
  ],
  "profile": { "name": "typicode" }
}`

func loadRulesOrFatal(t *testing.T, data string) []routes.Rule {
	t.Helper()
	rules, err := routes.LoadRules([]byte(data))
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	return rules
}

// TestLegacyPage 验证 --legacy 下 _page 返回数组并带 Link 与 X-Total-Count 头。
func TestLegacyPage(t *testing.T) {
	app, _ := newTestApp(t, legacyDB, Options{Legacy: true})
	status, head, body := doReq(app, "GET", "/posts?_page=2&_limit=1", "")
	if status != 200 {
		t.Fatalf("状态 %d", status)
	}
	if got := idsOf(t, body); !eqIDs(got, []string{"2"}) {
		t.Errorf("第 2 页应为 id 2: %s", body)
	}
	if head.Get("X-Total-Count") != "3" {
		t.Errorf("X-Total-Count = %q, 期望 3", head.Get("X-Total-Count"))
	}
	link := head.Get("Link")
	for _, want := range []string{`rel="next"`, `rel="last"`, `rel="prev"`, `rel="first"`} {
		if !strings.Contains(link, want) {
			t.Errorf("Link 头缺少 %s: %q", want, link)
		}
	}
	if !strings.Contains(link, "_page=3") {
		t.Errorf("Link 应包含下一页 _page=3: %q", link)
	}
}

// TestSliceParams 验证 _start/_end/_limit 的两套语义：
//   - --legacy（0.17 plural.js）：切片、_end 排他、X-Total-Count 为筛选后总数；
//   - 默认：v0.17 切片写法同样识别（v1 beta 把它们当字段过滤
//     返回空数组是 bug 而非特性，"和原版一致"不等于"复刻原版的 bug"）。
//     切片行为与 legacy 一致，仅 "-" 前缀方向按 v1 sortOn 语义处理。
func TestSliceParams(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		app, _ := newTestApp(t, legacyDB, Options{Legacy: true})
		tests := []struct {
			path    string
			want    []string
			wantXTC string // X-Total-Count 期望（空串表示不应存在）
		}{
			{"/posts?_limit=2", []string{"1", "2"}, "3"},
			{"/posts?_start=1&_end=2", []string{"2"}, "3"},       // _end 排他（Array.slice 语义）
			{"/posts?_limit=1&_sort=-views", []string{"1"}, "3"}, // 0.17 lodash orderBy 不识别 "-" 前缀，字段名 "-views" 取不到值 → 保持原序（实测）
			// 仅 _start：0.17 不切片也不发响应头（_end||_limit||_page 三者皆假）。
			{"/posts?_start=5", []string{"1", "2", "3"}, ""},
		}
		for _, tc := range tests {
			status, head, body := doReq(app, "GET", tc.path, "")
			if status != 200 {
				t.Fatalf("%s: 状态 %d", tc.path, status)
			}
			if got := idsOf(t, body); !eqIDs(got, tc.want) {
				t.Errorf("%s: id %v, 期望 %v", tc.path, got, tc.want)
			}
			if got := head.Get("X-Total-Count"); got != tc.wantXTC {
				t.Errorf("%s: X-Total-Count = %q, 期望 %q", tc.path, got, tc.wantXTC)
			}
		}
	})
	t.Run("default", func(t *testing.T) {
		// v1 beta 把这些写法当字段过滤返回空数组，不跟。
		app, _ := newTestApp(t, legacyDB, Options{})
		tests := []struct {
			path    string
			want    []string
			wantXTC string // X-Total-Count 期望（空串表示不应存在）
		}{
			{"/posts?_limit=2", []string{"1", "2"}, "3"},
			{"/posts?_start=1&_end=2", []string{"2"}, "3"},       // _end 排他
			{"/posts?_limit=1&_sort=-views", []string{"3"}, "3"}, // v1 "-" 前缀优先：降序 200,100,50 → 取 1 条为 id 3
			// 仅 _start：与 legacy 相同，不切片也不发响应头。
			{"/posts?_start=5", []string{"1", "2", "3"}, ""},
		}
		for _, tc := range tests {
			status, head, body := doReq(app, "GET", tc.path, "")
			if status != 200 {
				t.Fatalf("%s: 状态 %d", tc.path, status)
			}
			if got := idsOf(t, body); !eqIDs(got, tc.want) {
				t.Errorf("%s: id %v, 期望 %v", tc.path, got, tc.want)
			}
			if got := head.Get("X-Total-Count"); got != tc.wantXTC {
				t.Errorf("%s: X-Total-Count = %q, 期望 %q", tc.path, got, tc.wantXTC)
			}
		}
	})
}

// TestV017Operators 验证 v0.17 下划线写法在两种模式下的运算符集合：
//   - 默认（v1 parse-where splitKey）：下划线形式仅接受 v1 运算符表 lt/lte/gt/gte/eq/ne/in/
//     contains/startsWith/endsWith；like 不在表内。_like 结尾的键按与 --legacy
//     相同的正则语义处理（v1 beta 把 title_like 当整键等值查不到是 bug）；其余同键
//     多值取最后一个（eq/ne 严格大小写）。q 全文是本实现保留的有意差异。
//   - --legacy（0.17 plural.js 59-159）：仅 _lte/_gte/_ne/_like 后缀有效；
//     views_lt/gt 不在数据且无有效后缀 → 按"自动删除查询参数"整参忽略 → 全量。
//     0.17 的比较把查询串与字段值都按字符串/数字宽松比较（_ne 严格不等）。
func TestV017Operators(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		app, _ := newTestApp(t, legacyDB, Options{})
		tests := []struct {
			path string
			want []string
		}{
			{"/posts?views_gte=100", []string{"1", "3"}},
			{"/posts?views_lt=100", []string{"2"}}, // v1 下划线兼容表内运算符
			{"/posts?views_gt=100", []string{"3"}},
			{"/posts?views_lte=50", []string{"2"}},
			{"/posts?title_ne=first%20post", []string{"1", "2", "3"}}, // v1 ne 严格大小写
			{"/posts?title_like=^first", []string{"1"}},               // _like 同 legacy 正则（不区分大小写）
			{"/comments?body_like=comment$", []string{"1", "2"}},
			{"/posts?q=second", []string{"2"}}, // 有意差异：默认模式保留 q 全文
		}
		for _, tc := range tests {
			status, _, body := doReq(app, "GET", tc.path, "")
			if status != 200 {
				t.Fatalf("%s: 状态 %d", tc.path, status)
			}
			if got := idsOf(t, body); !eqIDs(got, tc.want) {
				t.Errorf("%s: id %v, 期望 %v", tc.path, got, tc.want)
			}
		}
	})
	t.Run("legacy", func(t *testing.T) {
		app, _ := newTestApp(t, legacyDB, Options{Legacy: true})
		tests := []struct {
			path string
			want []string
		}{
			{"/posts?views_gte=100", []string{"1", "3"}},
			{"/posts?views_lt=100", []string{"1", "2", "3"}}, // 0.17 无 _lt 后缀 → 整参删除
			{"/posts?views_gt=100", []string{"1", "2", "3"}},
			{"/posts?views_lte=50", []string{"2"}},
			{"/posts?title_ne=first%20post", []string{"1", "2", "3"}}, // 0.17 ne 按字符串严格不等
			{"/posts?title_like=^first", []string{"1"}},               // 正则
			{"/comments?body_like=comment$", []string{"1", "2"}},
			{"/posts?q=second", []string{"2"}}, // 全文
		}
		for _, tc := range tests {
			status, _, body := doReq(app, "GET", tc.path, "")
			if status != 200 {
				t.Fatalf("%s: 状态 %d", tc.path, status)
			}
			if got := idsOf(t, body); !eqIDs(got, tc.want) {
				t.Errorf("%s: id %v, 期望 %v", tc.path, got, tc.want)
			}
		}
	})
}

// TestNestedRoutes 验证嵌套路由（不带 --legacy 也识别）。
func TestNestedRoutes(t *testing.T) {
	app, path := newTestApp(t, legacyDB, Options{})
	// GET /posts/2/comments ≡ /comments?postId=2
	status, _, body := doReq(app, "GET", "/posts/2/comments", "")
	if status != 200 {
		t.Fatalf("状态 %d", status)
	}
	if got := idsOf(t, body); !eqIDs(got, []string{"2", "3"}) {
		t.Errorf("嵌套查询: %v", got)
	}
	// 可叠加查询参数：默认模式识别 _like（语义与 legacy 相同，正则不区分大小写）。
	_, _, body = doReq(app, "GET", "/posts/2/comments?body_like=another", "")
	if got := idsOf(t, body); !eqIDs(got, []string{"2"}) {
		t.Errorf("默认模式嵌套叠加条件: %v", got)
	}
	// legacy 模式下 _like 后缀同样生效。
	appL, _ := newTestApp(t, legacyDB, Options{Legacy: true})
	_, _, body = doReq(appL, "GET", "/posts/2/comments?body_like=another", "")
	if got := idsOf(t, body); !eqIDs(got, []string{"2"}) {
		t.Errorf("legacy 嵌套叠加条件: %v", got)
	}
	// POST 自动写入外键（取父记录 id 的原类型：数字）
	status, _, body = doReq(app, "POST", "/posts/1/comments", `{"body":"new"}`)
	if status != 201 {
		t.Fatalf("POST 状态 %d", status)
	}
	var rec map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &rec); err != nil {
		t.Fatal(err)
	}
	if string(rec["postId"]) != "1" {
		t.Errorf("postId 应为数字 1: %s", body)
	}
	if string(rec["id"]) == "" || string(rec["id"]) == "null" {
		t.Errorf("应生成 id: %s", body)
	}
	if n := arrayLen(t, path, "comments"); n != 4 {
		t.Errorf("comments 应为 4 条, 得到 %d", n)
	}
	// 不存在的父记录与子资源
	// 嵌套路由 ≡ 同名查询参数过滤（0.17），父记录不存在时默认模式也得 200 空数组。
	if status, _, body := doReq(app, "GET", "/posts/99/comments", ""); status != 200 || body != "[]" {
		t.Errorf("不存在的父记录应为 200 []，得到 %d %s", status, body)
	}
	if status, _, _ = doReq(app, "GET", "/posts/1/ghost", ""); status != 404 {
		t.Errorf("不存在的子资源应为 404, 得到 %d", status)
	}
}

// TestRoutesRewrite 验证 --routes 重写生效。
func TestRoutesRewrite(t *testing.T) {
	rules := loadRulesOrFatal(t, `{"/api/*": "/$1", "/blog/:cat": "/posts?q=:cat"}`)
	app, _ := newTestApp(t, legacyDB, Options{Routes: rules})
	// GET /api/posts → /posts
	status, _, body := doReq(app, "GET", "/api/posts", "")
	if status != 200 {
		t.Fatalf("状态 %d", status)
	}
	if got := idsOf(t, body); len(got) != 3 {
		t.Errorf("重写后应返回 3 条: %v", got)
	}
	// /blog/second → /posts?q=second（全文匹配 Second post）
	status, _, body = doReq(app, "GET", "/blog/second", "")
	if status != 200 {
		t.Fatalf("状态 %d", status)
	}
	if got := idsOf(t, body); !eqIDs(got, []string{"2"}) {
		t.Errorf("参数重写: %v", got)
	}
	// POST /api/comments 也应重写
	status, _, _ = doReq(app, "POST", "/api/comments", `{"body":"x","postId":1}`)
	if status != 201 {
		t.Errorf("POST 重写状态 %d", status)
	}
}

// TestExpand 验证 _expand 语义：两种模式都按 0.17 语义取父记录（v1 beta
// 把 _expand 当字段过滤返回空数组，不跟）。默认模式另由 requests-default-017 对照计划覆盖。
func TestExpand(t *testing.T) {
	app, _ := newTestApp(t, legacyDB, Options{})
	status, _, body := doReq(app, "GET", "/comments?_expand=post", "")
	if status != 200 {
		t.Fatalf("状态 %d", status)
	}
	if got := idsOf(t, body); !eqIDs(got, []string{"1", "2", "3"}) {
		t.Errorf("默认模式 _expand 应返回全部评论: %s", body)
	}
	if !strings.Contains(body, `"post"`) {
		t.Errorf("默认模式 _expand 应内嵌 post: %s", body)
	}
	appL, _ := newTestApp(t, legacyDB, Options{Legacy: true})
	status, _, body = doReq(appL, "GET", "/comments?_expand=post", "")
	if status != 200 {
		t.Fatalf("legacy 状态 %d", status)
	}
	if !strings.Contains(body, `"post"`) {
		t.Errorf("legacy _expand 应内嵌 post: %s", body)
	}
}

// TestLegacyDeleteFK 对照 0.17.4 实测：legacy 模式 DELETE 响应体为空对象，
// 嵌套 POST 的外键值写成字符串。
func TestLegacyDeleteFK(t *testing.T) {
	app, _ := newTestApp(t, legacyDB, Options{Legacy: true})
	_, _, body := doReq(app, "POST", "/posts/1/comments", `{"body":"n"}`)
	if !strings.Contains(body, `"postId":"1"`) {
		t.Errorf("legacy 嵌套 POST 外键应为字符串: %s", body)
	}
	status, _, body := doReq(app, "DELETE", "/posts/3", "")
	if status != 200 {
		t.Fatalf("DELETE 状态 %d", status)
	}
	if strings.TrimSpace(body) != "{}" {
		t.Errorf("legacy DELETE 响应体应为空对象: %q", body)
	}
}
