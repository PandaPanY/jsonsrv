package httpapi

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
	"github.com/PandaPanY/jsonsrv/internal/query"
)

// v1ReservedKeys 为基础集合（v1 app.js 的 RESERVED_QUERY_KEYS）
// 加上 R1重新纳入的 v0.17 写法：_start/_end/_limit/_order/_expand。
// v1 beta 把这些键当普通字段条件过滤导致返回空数组，属 bug 而非特性——
// "和原版一致"不等于"复刻原版的 bug"，因此默认模式必须识别这些写法。
var v1ReservedKeys = map[string]struct{}{
	"_sort": {}, "_page": {}, "_per_page": {}, "_embed": {}, "_where": {},
	"_start": {}, "_end": {}, "_limit": {}, "_order": {}, "_expand": {},
}

// legacyDeletedKeys 是 0.17 plural.js 在过滤前从 req.query 删除的键；
// _page 未被删除（仅因数据里没有该字段才被"自动删除参数"逻辑跳过）。
var legacyDeletedKeys = map[string]struct{}{
	"q": {}, "_start": {}, "_end": {}, "_sort": {}, "_order": {}, "_limit": {},
	"_embed": {}, "_expand": {}, "callback": {}, "_": {},
}

// buildMatch 把 URL 查询参数编译为过滤条件：
//   - 默认（v1）：按 lib/parse-where.js 把非保留参数编译为 where 树，再用 matches-where 匹配；
//     _where（JSON 对象）合法时整体替换参数树，非法/非对象时忽略回退参数树。
//     q 按 0.17 全文搜索（数字、布尔也算命中；v1 无 q 处理，属有意差异）；
//     普通等值参数的字段在数据里完全不存在时忽略该参数（与 0.17 一致，见 buildV1Match）。
//   - --legacy（0.17）：q 小写后做 deepQuery 全文；其余键按后缀运算符 _lte/_gte/_ne/_like
//     或等值 toString 比较；数据里查不到的键整参忽略（0.17"自动删除查询参数"）。
func (a *App) buildMatch(r *http.Request, records []jsonx.Value) (query.Match, error) {
	q := r.URL.Query()
	if a.opt.Legacy {
		return buildLegacyMatch(q, records), nil
	}
	return buildV1Match(q, records), nil
}

// buildV1Match 编译默认模式的过滤条件：v1 parse-where 语义为基线，叠加
// v0.17 写法（_like 正则、同名参数重复的 or/AND 语义）。
//
// 普通等值参数（field=value）的字段在所有记录里都不存在时忽略该参数，与 0.17 一致：
// 否则前端的防缓存参数（?_=1699、?t=1699）或多余参数会让列表静默变空。
// 显式的 field:op=value 不适用这条，字段不存在就是不匹配。
func buildV1Match(q url.Values, records []jsonx.Value) query.Match {
	var ands []query.Match
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tree := jsonx.NewObject()
	for _, key := range keys {
		if _, reserved := v1ReservedKeys[key]; reserved {
			continue
		}
		vals := q[key]
		if key == "q" {
			if vals[0] != "" { // q= 空值视为没有传
				ands = append(ands, query.FullTextLegacy(strings.ToLower(vals[0])))
			}
			continue
		}
		// v0.17 的 field_like 正则写法（与 --legacy 相同的语义：不区分大小写、
		// 同名重复全部参与、非法正则逐级退让）。v1 beta 把 title_like 当整键等值
		// 查不到是 bug。
		if strings.HasSuffix(key, "_like") {
			ands = append(ands, query.LegacyFieldMatch(key, vals))
			continue
		}
		path, op, ok := query.SplitV1Key(key)
		if !ok {
			continue // 冒号后是未知运算符：整个参数丢弃
		}
		hasColon := strings.Contains(key, ":")
		if !hasColon && op == "eq" && !query.LegacyHasParam(records, path) {
			continue
		}
		// 同名参数重复（0.17 plural.js 语义）：无冒号的下划线等值/ ne 写法里，
		// 多值全部生效——ne 之间 AND、其余 OR（id=1&id=2 命中其一）。带冒号的
		// v1 运算符（price_gte=… 等）维持 parse-where 的"最后一个生效"现状。
		if !hasColon && len(vals) > 1 && (op == "eq" || op == "ne") {
			ands = append(ands, buildV1RepeatMatch(path, op, vals))
			continue
		}
		// URLSearchParams 逐对 setProperty：同键多值时最后一个生效。
		query.SetParamToWhere(tree, path, op, vals[len(vals)-1])
	}

	// v1：_where 合法（对象/数组）时整体替换普通参数树；非法 JSON 或非对象时
	// 忽略并回退普通参数（v1 app.js parseListParams 的 catch 分支）。
	if raws, hasWhere := q["_where"]; hasWhere {
		if v, err := jsonx.Parse([]byte(raws[len(raws)-1])); err == nil {
			if m, valid := query.CompileWhereValue(v); valid {
				return query.AllOf(append(ands, m)...)
			}
		}
	}
	return query.AllOf(append(ands, query.CompileMatch(tree))...)
}

// buildV1RepeatMatch 实现同名参数重复语义（0.17 plural.js）：
// field=a&field=b 任一命中（或）；field_ne=1&field_ne=2 全部满足（AND）。
// 每个值按 v1 的运算符语义独立编译（含类型强转），互不干扰。
func buildV1RepeatMatch(path, op string, vals []string) query.Match {
	ms := make([]query.Match, 0, len(vals))
	for _, v := range vals {
		sub := jsonx.NewObject()
		query.SetParamToWhere(sub, path, op, v)
		ms = append(ms, query.CompileMatch(sub))
	}
	if op == "ne" {
		return query.AllOf(ms...)
	}
	return query.AnyOf(ms...)
}

// buildLegacyMatch 按 0.17 plural.js编译 legacy 过滤条件。
func buildLegacyMatch(q url.Values, records []jsonx.Value) query.Match {
	var ands []query.Match
	if raws, ok := q["q"]; ok {
		s := raws[0] // express qs 多值取第一个；空串 falsy 不过滤
		if s != "" {
			ands = append(ands, query.FullTextLegacy(strings.ToLower(s)))
		}
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, deleted := legacyDeletedKeys[key]; deleted {
			continue
		}
		if !legacySuffixed(key) && !query.LegacyHasParam(records, key) {
			continue // 0.17：数据里查不到的键整参删除
		}
		ands = append(ands, query.LegacyFieldMatch(key, q[key]))
	}
	return query.AllOf(ands...)
}

// legacySuffixed 对应 0.17 的 /_lte$/、/_gte$/、/_ne$/、/_like$ 测试（后缀键不做存在性检查）。
func legacySuffixed(key string) bool {
	for _, suf := range []string{"_lte", "_gte", "_ne", "_like"} {
		if strings.HasSuffix(key, suf) {
			return true
		}
	}
	return false
}
