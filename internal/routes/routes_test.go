package routes

import (
	"net/url"
	"testing"
)

func loadRules(t *testing.T, data string) []Rule {
	t.Helper()
	rules, err := LoadRules([]byte(data))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	return rules
}

func rewrite(t *testing.T, rules []Rule, target string) (string, string, bool) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	segs, q, ok := Rewrite(rules, splitSegments(u.Path), u.Query())
	if !ok {
		return "", "", false
	}
	return "/" + join(segs), q.Encode(), true
}

func join(segs []string) string {
	var s string
	for i, seg := range segs {
		if i > 0 {
			s += "/" + seg
		} else {
			s = seg
		}
	}
	return s
}

func TestWildcardRewrite(t *testing.T) {
	rules := loadRules(t, `{"/api/*": "/$1"}`)
	tests := []struct {
		target, wantPath, wantQuery string
		wantOK                      bool
	}{
		{"/api/posts", "/posts", "", true},
		{"/api/posts/1", "/posts/1", "", true},
		{"/api/posts?_sort=id", "/posts", "_sort=id", true},
		{"/api/a/b/c", "/a/b/c", "", true},
		{"/posts", "", "", false},
		{"/other", "", "", false},
	}
	for _, tc := range tests {
		p, q, ok := rewrite(t, rules, tc.target)
		if ok != tc.wantOK {
			t.Errorf("%s: ok=%v, 期望 %v", tc.target, ok, tc.wantOK)
			continue
		}
		if ok && (p != tc.wantPath || q != tc.wantQuery) {
			t.Errorf("%s: %s?%s, 期望 %s?%s", tc.target, p, q, tc.wantPath, tc.wantQuery)
		}
	}
}

func TestParamRewrite(t *testing.T) {
	rules := loadRules(t, `{"/blog/:category": "/posts?category=:category"}`)
	// 实测（0.17）：目标带 "?" 时其查询整体替换原查询（原 limit 参数被丢弃）。
	p, q, ok := rewrite(t, rules, "/blog/json?limit=3")
	if !ok || p != "/posts" || q != "category=json" {
		t.Errorf("参数重写: %s?%s ok=%v", p, q, ok)
	}
	// 目标参数覆盖同名原参数
	if _, q, _ := rewrite(t, rules, "/blog/json?category=old"); q != "category=json" {
		t.Errorf("目标参数应覆盖原参数: %s", q)
	}
}

// TestChainRewrite 对照 express-urlrewrite（lib/server/rewriter.js 逐规则 router.use
// 链，实测 f6-p8）：命中规则后的 URL 继续参与后续规则的匹配。
func TestChainRewrite(t *testing.T) {
	rules := loadRules(t, `{"/api/*": "/posts/1", "/posts/:category": "/posts?category=:category"}`)
	// GET /api/posts/1 先重写为 /posts/1，再被第二条规则改写为 /posts?category=1。
	p, q, ok := rewrite(t, rules, "/api/posts/1")
	if !ok || p != "/posts" || q != "category=1" {
		t.Errorf("链式重写: %s?%s ok=%v", p, q, ok)
	}
	// 重写目标的 "?" 查询替换了原查询：_embed 不应存活（实测返全量列表且无 comments 键）。
	p, q, ok = rewrite(t, rules, "/posts/1?_embed=comments")
	if !ok || p != "/posts" || q != "category=1" {
		t.Errorf("重写后查询替换: %s?%s ok=%v", p, q, ok)
	}
	// 链未命中第二条规则时保持第一条重写结果。
	rules2 := loadRules(t, `{"/api/*": "/posts/1"}`)
	if p, _, ok := rewrite(t, rules2, "/api/posts/1"); !ok || p != "/posts/1" {
		t.Errorf("单规则重写: %s ok=%v", p, ok)
	}
}

func TestRewriteOrderAndMerge(t *testing.T) {
	// 先匹配的规则生效
	rules := loadRules(t, `{"/a/*": "/x", "/a/:id": "/y/:id"}`)
	if p, _, ok := rewrite(t, rules, "/a/1"); !ok || p != "/x" {
		t.Errorf("应按键顺序匹配: %s", p)
	}
	// 仅 ":id" 单独段
	rules2 := loadRules(t, `{"/posts/:id/comments": "/comments?postId=:id"}`)
	p, q, ok := rewrite(t, rules2, "/posts/7/comments")
	if !ok || p != "/comments" || q != "postId=7" {
		t.Errorf("嵌套重写: %s?%s ok=%v", p, q, ok)
	}
}

func TestLoadRulesErrors(t *testing.T) {
	if _, err := LoadRules([]byte(`[1,2]`)); err == nil {
		t.Error("顶层应为对象")
	}
	if _, err := LoadRules([]byte(`{"/a": 5}`)); err == nil {
		t.Error("目标应为字符串")
	}
	if _, err := LoadRules([]byte(`{bad`)); err == nil {
		t.Error("非法 JSON 应报错")
	}
}

// TestQueryAssertionPattern 覆盖 0.17 README 第 4 条：
// "/articles\\?id=:id": "/posts/:id" 这样的查询串断言/捕获。
func TestQueryAssertionPattern(t *testing.T) {
	rules := loadRules(t, `{"/articles\\?id=:id": "/posts/:id"}`)
	// 断言命中：查询里必须有 id，值捕获到 :id
	if p, _, ok := rewrite(t, rules, "/articles?id=42"); !ok || p != "/posts/42" {
		t.Errorf("查询捕获重写: p=%q ok=%v", p, ok)
	}
	// 缺少查询键：不匹配
	if _, _, ok := rewrite(t, rules, "/articles"); ok {
		t.Error("缺少 id 查询键应不匹配")
	}
	// 查询里有其他参数也要能命中（断言只要求 id 存在）
	if p, _, ok := rewrite(t, rules, "/articles?_sort=title&id=7"); !ok || p != "/posts/7" {
		t.Errorf("混合查询: p=%q ok=%v", p, ok)
	}
	// 裸 "?" 写法同样支持
	rules2 := loadRules(t, `{"/x?id=:id": "/y/:id"}`)
	if p, _, ok := rewrite(t, rules2, "/x?id=9"); !ok || p != "/y/9" {
		t.Errorf("裸问号写法: p=%q ok=%v", p, ok)
	}
	// 字面断言：值必须相等
	rules3 := loadRules(t, `{"/list?type=all": "/all"}`)
	if p, _, ok := rewrite(t, rules3, "/list?type=all"); !ok || p != "/all" {
		t.Errorf("字面断言命中: p=%q ok=%v", p, ok)
	}
	if _, _, ok := rewrite(t, rules3, "/list?type=part"); ok {
		t.Error("字面断言不匹配应拒绝")
	}
}

// TestMultiWildcardSplats 覆盖多个通配符（$1、$2）。
func TestMultiWildcardSplats(t *testing.T) {
	rules := loadRules(t, `{"/a/*/b/*": "/$2/$1"}`)
	if p, _, ok := rewrite(t, rules, "/a/1/2/b/3/4"); !ok || p != "/3/4/1/2" {
		t.Errorf("多通配捕获: p=%q ok=%v", p, ok)
	}
	// 尾部通配为空也允许（0.17 path-to-regexp 的 (.*) 匹配空）
	if p, _, ok := rewrite(t, rules, "/a/x/b/"); !ok || p != "/x" {
		t.Errorf("空通配尾部: p=%q ok=%v", p, ok)
	}
}

// TestParamReplaceLongestFirst :idx 与 :id 同时存在时按名字长度从长到短替换，
// 不再依赖 map 遍历顺序。
func TestParamReplaceLongestFirst(t *testing.T) {
	rules := loadRules(t, `{"/u/:id/:idx": "/p/:id/:idx"}`)
	p, _, ok := rewrite(t, rules, "/u/1/22")
	if !ok || p != "/p/1/22" {
		t.Errorf("双参数替换: p=%q ok=%v", p, ok)
	}
	// 查询目标里同名长参数不能被短参数前缀吞噬
	rules2 := loadRules(t, `{"/s/:id/:idx": "/q?idx=:idx&id=:id"}`)
	_, q, ok := rewrite(t, rules2, "/s/1/22")
	if !ok || q != "id=1&idx=22" {
		t.Errorf("长名优先: q=%q ok=%v", q, ok)
	}
}

// TestQueryInjectionEscape 替换值注入查询串时转义 &、= 和 #。
func TestQueryInjectionEscape(t *testing.T) {
	rules := loadRules(t, `{"/blog/:slug": "/posts?search=:slug"}`)
	_, q, ok := rewrite(t, rules, "/blog/a%26b%3Dc")
	if !ok {
		t.Fatal("未命中")
	}
	// url.Query() 已把 %26 解码为 &，写回查询串时必须重新转义：
	// 解码查询串后 search 应恰好是一个值 "a&b=c"。
	vals, err := url.ParseQuery(q)
	if err != nil {
		t.Fatalf("重写后的查询串应可解析: %q (%v)", q, err)
	}
	if len(vals["search"]) != 1 || vals["search"][0] != "a&b=c" {
		t.Errorf("注入应被转义为单值: q=%q vals=%v", q, vals["search"])
	}
}
