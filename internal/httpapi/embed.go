package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/PandaPanY/jsonsrv/internal/inflect"
	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// queryValuesAll 收集查询里同名多值的全部取值（重复参数全部生效），
// 逗号分隔的写法继续支持。
func queryValuesAll(q url.Values, keys ...string) []string {
	var out []string
	for _, k := range keys {
		for _, v := range q[k] {
			out = append(out, splitComma(v)...)
		}
	}
	return out
}

// wantsEmbed 报告请求是否带有可触发嵌入的参数。
// 默认模式同样识别 _expand（v1 beta 把 _expand 当字段过滤属 bug，
// 与 --legacy 都不一致）。--legacy 一直识别两者。
func (a *App) wantsEmbed(r *http.Request) bool {
	q := r.URL.Query()
	return len(q["_embed"]) > 0 || len(q["_expand"]) > 0
}

// embedExpandNames 返回（_embed 名单，_expand 名单），两种模式都认 _expand。
// 同名参数重复出现时全部生效。
func (a *App) embedExpandNames(r *http.Request) ([]string, []string) {
	q := r.URL.Query()
	return queryValuesAll(q, "_embed"), queryValuesAll(q, "_expand")
}

// applyEmbeds 处理 _embed/_expand：为结果附加关联数据（返回深拷贝的新记录）。
//
//	_embed=comments：把以"本资源单数+外键后缀"指向本集合的记录挂到每条记录上（子嵌入）；
//	_embed=author：按"关系名单数+外键后缀"到目标集合里取父记录（父嵌入，v1）。
//
// _expand（v0.17 专属）：按"关系名+外键后缀"取父记录。
//
// 每个请求一个 embedHelper，按需在目标集合上建外键/主键索引，
// 把 _embed 的复杂度从"窗口 × 全表"降为"一次建索引 O(全表) + O(1) 查找"。
func (a *App) applyEmbeds(r *http.Request, resID string, items []jsonx.Value) []jsonx.Value {
	embeds, expands := a.embedExpandNames(r)
	if len(embeds) == 0 && len(expands) == 0 {
		return items
	}
	h := &embedHelper{app: a, db: a.st.Snapshot(),
		children: map[string]map[string][]jsonx.Value{},
		parents:  map[string]map[string]jsonx.Value{}}
	out := make([]jsonx.Value, len(items))
	for i, rec := range items {
		obj, ok := jsonx.AsObject(rec)
		if !ok {
			out[i] = rec
			continue
		}
		clone := obj.Clone()
		for _, name := range embeds {
			h.embedOne(resID, clone, name)
		}
		for _, name := range expands {
			h.expandOne(clone, name)
		}
		out[i] = jsonx.Value(clone)
	}
	return out
}

// embedHelper 是单请求的嵌入辅助：缓存按需构建的关联索引。
type embedHelper struct {
	app *App
	db  *jsonx.Object
	// children 键为 "集合名\x00外键字段"，值为"外键取值 → 子记录列表"。
	children map[string]map[string][]jsonx.Value
	// parents 键为集合名，值为"主键取值 → 记录"（按 findRecord 语义：首个命中）。
	parents map[string]map[string]jsonx.Value
}

// childIndex 返回集合按外键字段分组的子记录索引（缺失键/非标量的记录不入索引，
// 与 recordHasValue 的判定一致）。
func (h *embedHelper) childIndex(collName, fk string) map[string][]jsonx.Value {
	key := collName + "\x00" + fk
	if idx, ok := h.children[key]; ok {
		return idx
	}
	idx := map[string][]jsonx.Value{}
	if arr, ok := jsonx.AsArray(h.db.MustGet(collName)); ok {
		for _, rec := range arr {
			obj, ok := jsonx.AsObject(rec)
			if !ok {
				continue
			}
			v, ok := obj.Get(fk)
			if !ok {
				continue
			}
			s, ok := jsonx.ToString(v)
			if !ok {
				continue
			}
			idx[s] = append(idx[s], rec)
		}
	}
	h.children[key] = idx
	return idx
}

// parentIndex 返回集合按 id 的主键索引（与 findRecord 的匹配语义一致：
// 字符串与数字 id 均可命中，重复 id 保留首个；其余记录不入索引）。
func (h *embedHelper) parentIndex(collName string) map[string]jsonx.Value {
	if idx, ok := h.parents[collName]; ok {
		return idx
	}
	idx := map[string]jsonx.Value{}
	if arr, ok := jsonx.AsArray(h.db.MustGet(collName)); ok {
		for _, rec := range arr {
			obj, ok := jsonx.AsObject(rec)
			if !ok {
				continue
			}
			var s string
			switch id := obj.MustGet(h.app.opt.IDName).(type) {
			case string:
				s = id
			case json.Number:
				s = id.String()
			default:
				continue
			}
			if _, dup := idx[s]; !dup {
				idx[s] = rec
			}
		}
	}
	h.parents[collName] = idx
	return idx
}

// expandOne 实现 0.17 plural.js expand()：目标集合 = 名字复数化；
// 外键字段 = 名字原样 + 后缀（article → articleId）；命中则挂父记录，
// 未命中或目标集合不存在时不写该键（0.17 设 undefined，序列化后消失）。
func (h *embedHelper) expandOne(rec *jsonx.Object, name string) {
	targetName := inflect.Plural(name)
	if _, ok := jsonx.AsArray(h.db.MustGet(targetName)); !ok {
		return
	}
	fkv, ok := rec.Get(name + h.app.opt.FKS)
	if !ok {
		return
	}
	fkStr, ok := jsonx.ToString(fkv)
	if !ok {
		return
	}
	if rec2, ok := h.parentIndex(targetName)[fkStr]; ok {
		rec.Set(name, jsonx.DeepClone(rec2))
	}
}

// embedOne 处理单个 _embed 名：
// --legacy（0.17 plural.js embed()）：只做子嵌入（目标集合=参数名），
// 目标集合不存在时写空数组（实测 `"nope": []`）。
// 默认（v1）：先按"目标集合名存在且不是自己"做子嵌入，
// 否则尝试父嵌入（找关系名复数化的集合）。
func (h *embedHelper) embedOne(resID string, rec *jsonx.Object, name string) {
	a := h.app
	if a.opt.Legacy {
		fk := inflect.Singular(resID) + a.opt.FKS
		idv, _ := rec.Get(a.opt.IDName)
		idStr, _ := jsonx.ToString(idv)
		matches := []jsonx.Value{}
		if _, ok := jsonx.AsArray(h.db.MustGet(name)); ok {
			matches = append(matches, h.childIndex(name, fk)[idStr]...)
		}
		rec.Set(name, jsonx.Value(matches))
		return
	}
	if name != resID {
		if _, ok := jsonx.AsArray(h.db.MustGet(name)); ok {
			fk := inflect.Singular(resID) + a.opt.FKS
			idv, _ := rec.Get(a.opt.IDName)
			idStr, _ := jsonx.ToString(idv)
			matches := []jsonx.Value{}
			matches = append(matches, h.childIndex(name, fk)[idStr]...)
			rec.Set(name, jsonx.Value(matches))
			return
		}
	}
	// 父嵌入：name=post 找 posts 集合，外键为 name单数+后缀。
	col := a.relationCollection(h.db, name)
	if _, ok := jsonx.AsArray(h.db.MustGet(col)); !ok {
		return // 集合不存在：不写该键（原实现直接返回）
	}
	fk := inflect.Singular(name) + a.opt.FKS
	idv, ok := rec.Get(fk)
	if !ok {
		return
	}
	idStr, _ := jsonx.ToString(idv)
	if cand, ok := h.parentIndex(col)[idStr]; ok {
		rec.Set(name, jsonx.DeepClone(cand))
		return
	}
	rec.Set(name, nil) // 指向不存在的记录：显式 null
}

// relationCollection 猜测嵌入关系对应的集合名：name 的复数形式，
// 或数据里已有的以 name 结尾的复数资源。
func (a *App) relationCollection(db *jsonx.Object, name string) string {
	candidates := []string{inflect.Plural(name), name}
	for _, key := range db.Keys() {
		for _, c := range candidates {
			if key == c {
				return key
			}
		}
	}
	return inflect.Plural(name)
}
