// Package routes 实现 json-server v0.17 的 --routes 文件路径重写。
// 规则形如 "/api/*": "/$1"、"/blog/:category": "/posts?category=:category"，
// 也支持 0.17 README 第 4 条的查询串写法 "/articles\\?id=:id": "/posts/:id"
// （express-urlrewrite + path-to-regexp 语义）。
package routes

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// Rule 是一条已解析的重写规则。
type Rule struct {
	pattern []patternSeg
	// queryParts 为模式 "?" 之后的查询断言（0.17 "/articles\?id=:id"）。
	queryParts []queryPart
	target     string
}

type patternSeg struct {
	literal  string
	param    string // ":name" 捕获单段
	wildcard bool   // "*" 捕获剩余（若有后续字面段则捕获到该段之前）
}

// queryPart 是模式查询段的断言/捕获。
type queryPart struct {
	key   string
	param string // ":name"：捕获该查询键的值
	want  string // 字面量（含空串）：该查询键必须等于此值；param 为空即字面断言
}

// LoadRules 解析 --routes 文件内容（JSON 对象，键顺序即匹配顺序）。
func LoadRules(data []byte) ([]Rule, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("cannot parse routes file: %w", err)
	}
	obj, ok := jsonx.AsObject(v)
	if !ok {
		return nil, fmt.Errorf("routes file must contain a JSON object")
	}
	rules := make([]Rule, 0, obj.Len())
	for _, key := range obj.Keys() {
		val, _ := obj.Get(key)
		target, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("rule %q: rewrite target must be a string", key)
		}
		pat, qparts, err := parsePattern(key)
		if err != nil {
			return nil, err
		}
		rules = append(rules, Rule{pattern: pat, queryParts: qparts, target: target})
	}
	return rules, nil
}

// parsePattern 拆模式串："\\?"（或裸 "?"）之后为查询断言段。
func parsePattern(p string) ([]patternSeg, []queryPart, error) {
	queryTmpl := ""
	if i := strings.Index(p, `\?`); i >= 0 {
		queryTmpl = p[i+2:]
		p = p[:i]
	} else if i := strings.IndexByte(p, '?'); i >= 0 {
		queryTmpl = p[i+1:]
		p = p[:i]
	}
	var segs []patternSeg
	for _, s := range splitSegments(p) {
		switch {
		case s == "*":
			segs = append(segs, patternSeg{wildcard: true})
		case len(s) > 1 && s[0] == ':':
			segs = append(segs, patternSeg{param: s[1:]})
		default:
			segs = append(segs, patternSeg{literal: s})
		}
	}
	var qparts []queryPart
	for _, part := range strings.Split(queryTmpl, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		if k == "" {
			continue
		}
		if strings.HasPrefix(v, ":") && len(v) > 1 {
			qparts = append(qparts, queryPart{key: k, param: v[1:]})
		} else {
			qparts = append(qparts, queryPart{key: k, want: v})
		}
	}
	return segs, qparts, nil
}

func splitSegments(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	out := parts[:0]
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Rewrite 按规则依次匹配路径段与查询，返回重写后的路径段与查询参数；
// 无规则命中时返回 ok=false。
//
// 与 express-urlrewrite（0.17 lib/server/rewriter.js 逐规则 router.use 链）
// 对齐的两点语义（实测）：
//   - 链式生效：命中规则后重写结果继续参与后续规则的匹配（如
//     "/api/*"→"/posts/1" 再被 "/posts/:category" 改写为全量列表）；
//   - 目标 URL 带 "?" 时其查询整体替换原查询（实测 GET /posts/1?_embed=comments
//     重写后 _embed 丢失、不再带 comments 键）；目标不带 "?" 时保留原查询
//     （对应 "/" 通配 "$1" 贪婪捕获原查询的原版常见写法，效果一致）。
func Rewrite(rules []Rule, segs []string, q url.Values) ([]string, url.Values, bool) {
	ok := false
	for _, ru := range rules {
		if out, outq, hit := ru.apply(segs, q); hit {
			segs, q = out, outq
			ok = true
		}
	}
	if !ok {
		return nil, nil, false
	}
	return segs, q, true
}

// apply 应用单条规则。
func (ru Rule) apply(segs []string, q url.Values) ([]string, url.Values, bool) {
	params := make(map[string]string)
	var splats []string
	i := 0
	for idx, ps := range ru.pattern {
		if ps.wildcard {
			// 有后续字面段则捕获到该段之前（支持多通配 $1、$2），否则捕获全部剩余。
			nextLit := ""
			for k := idx + 1; k < len(ru.pattern); k++ {
				if ru.pattern[k].literal != "" {
					nextLit = ru.pattern[k].literal
					break
				}
				if ru.pattern[k].param != "" || ru.pattern[k].wildcard {
					break
				}
			}
			if nextLit != "" {
				j := i
				for j < len(segs) && segs[j] != nextLit {
					j++
				}
				if j >= len(segs) {
					return nil, nil, false
				}
				splats = append(splats, strings.Join(segs[i:j], "/"))
				i = j
			} else {
				splats = append(splats, strings.Join(segs[i:], "/"))
				i = len(segs)
			}
			continue
		}
		if i >= len(segs) {
			return nil, nil, false
		}
		if ps.param != "" {
			params[ps.param] = segs[i]
		} else if ps.literal != segs[i] {
			return nil, nil, false
		}
		i++
	}
	if i != len(segs) {
		return nil, nil, false
	}

	// 查询断言/捕获（0.17 "path\?key=:name"）。
	for _, qp := range ru.queryParts {
		vals, ok := q[qp.key]
		if !ok || len(vals) == 0 {
			return nil, nil, false
		}
		if qp.param != "" {
			params[qp.param] = vals[0]
			continue
		}
		if vals[0] != qp.want {
			return nil, nil, false
		}
	}

	// 目标替换：查询段里的注入值转义（防止 &、= 注入查询串），路径段保持原样。
	targetPath, targetQuery, hasQuery := strings.Cut(ru.target, "?")
	replace := func(s string, inQuery bool) string {
		// $N：按数字位数从长到短（先 $12 再 $1），避免前缀吞噬。
		for n := len(splats); n >= 1; n-- {
			from := "$" + strconv.Itoa(n)
			to := splats[n-1]
			if inQuery {
				to = escapeQueryValue(to)
			}
			s = strings.ReplaceAll(s, from, to)
		}
		// :name：按名字长度从长到短（:idx 先于 :id，消除 map 顺序随机）。
		names := make([]string, 0, len(params))
		for name := range params {
			names = append(names, name)
		}
		sort.Slice(names, func(a, b int) bool {
			return len(names[a]) > len(names[b])
		})
		for _, name := range names {
			to := params[name]
			if inQuery {
				to = escapeQueryValue(to)
			}
			s = strings.ReplaceAll(s, ":"+name, to)
		}
		return s
	}

	// 查询：目标带 "?" 时整体替换原查询（实测：express-urlrewrite 重写后
	// 用新 URL 重算 req.query，原查询参数如 _embed 被丢弃）；目标不带 "?" 时
	// 保留原查询（原版此时查询串随 $1 通配捕获重建进目标 URL，效果一致）。
	if hasQuery {
		outq := url.Values{}
		if vs, err := url.ParseQuery(replace(targetQuery, true)); err == nil {
			outq = vs
		}
		return splitSegments(replace(targetPath, false)), outq, true
	}
	return splitSegments(replace(targetPath, false)), q, true
}

// escapeQueryValue 转义替换进查询串的值，防止 &、=、# 注入新的参数边界。
func escapeQueryValue(s string) string {
	r := strings.NewReplacer("&", "%26", "=", "%3D", "#", "%23")
	return r.Replace(s)
}
