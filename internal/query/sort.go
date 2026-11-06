package query

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// Sort 按 spec（如 "views,-id,author.name"）稳定排序 docs（原地），默认 v1 sort-on 语义。
func Sort(docs []jsonx.Value, spec string) { SortWithOrder(docs, spec, "", false) }

// SortWithOrder 稳定排序 docs（原地），按模式选择比较语义：
//   - 默认（v1）：sortOn（@sindresorhus/sort-on）——空值（null/""/false/缺失）
//     挤到末尾（数字 0 不算空）、缺失与 null 之间保持输入顺序、字符串按小写不区分大小写比较
//     （localeCompare 的 ASCII 近似）；字段名带 "-" 前缀表示降序。
//     _order 参数同样按位置应用到 _sort 的字段上（v0.17 写法），
//     显式 "-" 前缀的方向优先于 _order。
//   - legacy（v0.17）：lodash orderBy——null/undefined 排最后且相互稳定、字符串按字节序、
//     双方数字按数值、其余按 JS 关系比较；方向只由 _order 参数按位置决定，
//     缺省位置为 asc，"-" 前缀不作为方向（此时字段名含 "-" 取不到值）。
//
// 比较前为每条记录抽取一次排序键（装饰排序），避免热路径重复取值。
func SortWithOrder(docs []jsonx.Value, spec, order string, legacy bool) {
	sorters := parseSortSpec(spec, legacy)
	if len(sorters) == 0 {
		return
	}
	if legacy {
		applyLegacyOrder(sorters, order)
	} else if order != "" {
		applyV1Order(sorters, order)
	}
	keys := make([][]sortKey, len(docs))
	for i, d := range docs {
		ks := make([]sortKey, len(sorters))
		for j, s := range sorters {
			var v jsonx.Value
			var found bool
			if legacy {
				v, found = lodashGet(d, s.parts)
			} else {
				v, found = GetPathParts(d, s.parts)
			}
			ks[j] = newSortKey(v, found)
		}
		keys[i] = ks
	}
	idx := make([]int, len(docs))
	for i := range idx {
		idx[i] = i
	}
	// 具名 sort.Interface：避免 sort.SliceStable 反射包装的每次比较开销（多字段排序的热点）。
	sort.Stable(&idxSorter{idx: idx, keys: keys, sorters: sorters, legacy: legacy})
	out := make([]jsonx.Value, len(docs))
	for pos, src := range idx {
		out[pos] = docs[src]
	}
	copy(docs, out)
}

// idxSorter 对 idx（docs 下标序列）按预抽取的 keys 做稳定排序比较（性能热点）。
type idxSorter struct {
	idx     []int
	keys    [][]sortKey
	sorters []fieldSorter
	legacy  bool
}

func (x *idxSorter) Len() int { return len(x.idx) }

func (x *idxSorter) Swap(i, j int) { x.idx[i], x.idx[j] = x.idx[j], x.idx[i] }

func (x *idxSorter) Less(i, j int) bool {
	ka, kb := x.keys[x.idx[i]], x.keys[x.idx[j]]
	if x.legacy {
		for s := range x.sorters {
			if c := cmpOrderBy(ka[s], kb[s]); c != 0 {
				return x.sorters[s].desc == (c > 0)
			}
		}
		return false
	}
	for s := range x.sorters {
		if c := cmpSortOn(ka[s], kb[s]); c != 0 {
			return x.sorters[s].desc == (c > 0)
		}
	}
	return false
}

// applyLegacyOrder 按 v0.17 语义把 _order 应用到排序字段：
// 逗号分隔方向与字段按位置对应（orders 转小写、不 trim），非 "desc" 一律升序，
// 缺省位置也是升序（lodash orderBy 行为）。
func applyLegacyOrder(sorters []fieldSorter, order string) {
	orders := strings.Split(order, ",")
	for i := range sorters {
		dir := ""
		if i < len(orders) {
			dir = strings.ToLower(orders[i])
		}
		sorters[i].desc = dir == "desc"
	}
}

// applyV1Order 在默认模式按位置应用 _order（v0.17 写法）：逗号分隔的方向
// 与 _sort 字段一一对应，"desc" 降序、其余升序；字段名带显式 "-" 前缀时保持
// 前缀方向，不被 _order 覆盖。v1 beta 不识别 _order（当字段过滤）是 bug 行为。
func applyV1Order(sorters []fieldSorter, order string) {
	orders := strings.Split(order, ",")
	for i := range sorters {
		if sorters[i].explicit {
			continue
		}
		if i < len(orders) && strings.ToLower(strings.TrimSpace(orders[i])) == "desc" {
			sorters[i].desc = true
		}
	}
}

type fieldSorter struct {
	field    string
	parts    []string // 预拆分的字段路径（v1 点号；legacy lodash 路径）
	desc     bool
	explicit bool // 字段名是否显式带 "-" 前缀（仅 v1 模式）
}

// parseSortSpec 解析 "_sort" 参数为排序字段列表。
// legacy 模式不解释 "-" 前缀（lodash 会把它当字段名的一部分）。
func parseSortSpec(spec string, legacy bool) []fieldSorter {
	var out []fieldSorter
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !legacy && part[0] == '-' {
			if field := strings.TrimSpace(part[1:]); field != "" {
				out = append(out, fieldSorter{field: field, parts: strings.Split(field, "."), desc: true, explicit: true})
			}
			continue
		}
		parts := strings.Split(part, ".")
		if legacy {
			parts = lodashPathParts(part)
		}
		out = append(out, fieldSorter{field: part, parts: parts})
	}
	return out
}

// sortKey 是预抽取的排序键：缺失标记、值本身与其派生形式（小写文本、数值、空值判定）。
type sortKey struct {
	found bool
	val   jsonx.Value
	isStr bool
	lower string // 字符串的小写形式（v1 localeCompare 近似）
	isNum bool
	num   float64
	// isEmpty 对应 sortOn 的 `y !== 0 && !y`：缺失或假值，但数字 0 不算空。
	isEmpty bool
}

func newSortKey(v jsonx.Value, found bool) sortKey {
	k := sortKey{found: found, val: v}
	if found {
		switch tv := v.(type) {
		case string:
			k.isStr = true
			k.lower = strings.ToLower(tv)
		case json.Number:
			if f, err := tv.Float64(); err == nil {
				k.isNum = true
				k.num = f
			}
		}
		k.isEmpty = !jsTruthy(v) && !(k.isNum && k.num == 0)
	} else {
		k.isEmpty = true
	}
	return k
}

// keysStrictEq 对应 sortOn 的 `x === y`：双方缺失（undefined）相等；其余为 JS 严格相等。
// 数值/字符串使用预抽取键，避免每次比较重新 ParseFloat（性能修复，语义与 jsStrictEq 一致：
// 双方数值时 Float64 必成功、数值相等即 ===；双方字符串按原值比较）。
func keysStrictEq(a, b sortKey) bool {
	if !a.found || !b.found {
		return !a.found && !b.found
	}
	if a.isNum && b.isNum {
		return a.num == b.num
	}
	if a.isStr || b.isStr {
		if a.isStr && b.isStr {
			return a.val == b.val
		}
		return false // 数值与字符串、字符串与其余类型：=== 恒 false
	}
	return jsStrictEq(a.val, b.val) // 布尔/null/对象分支无浮点解析，保持原实现
}

// cmpSortOn 单字段比较，忠实移植 sort-on 的分支顺序。
// 返回 -1/0/1；比较结果依赖稳定排序保持输入顺序的场景（空值之间、NaN）返回 0。
func cmpSortOn(a, b sortKey) int {
	if keysStrictEq(a, b) {
		return 0
	}
	// 源码先判 y 空再判 x 空：y 空 → a 在前；x 空（y 非空）→ a 在后。
	if b.isEmpty {
		return -1
	}
	if a.isEmpty {
		return 1
	}
	if a.isStr && b.isStr {
		return strings.Compare(a.lower, b.lower)
	}
	if a.isNum && b.isNum {
		return relNumCmp(a.num, b.num) // 预抽取数值，避免重复 ParseFloat
	}
	c, ok := jsRelCmp(a.val, b.val)
	if !ok {
		return 0 // NaN 比较为 false：两个方向都不小于，保持原序
	}
	return c
}

// cmpOrderBy 单字段比较，对应 lodash orderBy（实测：null/缺失最后且相互稳定、
// 字符串按字节、数字按数值、其余 JS 关系比较）。
func cmpOrderBy(a, b sortKey) int {
	aNil := !a.found || a.val == nil
	bNil := !b.found || b.val == nil
	switch {
	case aNil && bNil:
		return 0
	case aNil:
		return 1
	case bNil:
		return -1
	}
	if a.isNum && b.isNum {
		switch {
		case a.num < b.num:
			return -1
		case a.num > b.num:
			return 1
		}
		return 0
	}
	c, ok := jsRelCmp(a.val, b.val)
	if !ok {
		return 0
	}
	return c
}
