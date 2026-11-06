// Package query 实现 v1 与 v0.17 的查询语义：条件匹配（matches-where）、
// 查询参数解析（parse-where）、legacy 过滤（lodash 风格）与全文匹配（deepQuery）。
// 比较与类型转换均为对应 JS 实现的忠实移植（=== 严格比较、关系比较 < <= > >=、
// ToNumber、toString），依据原版源码与实测。
package query

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// Match 判断一条记录是否满足条件。
type Match func(v jsonx.Value) bool

// AllOf 全部条件满足。
func AllOf(ms ...Match) Match {
	return func(v jsonx.Value) bool {
		for _, m := range ms {
			if !m(v) {
				return false
			}
		}
		return true
	}
}

// AnyOf 任一条件满足；空列表恒假（与低代码库 or: [] 语义一致）。
func AnyOf(ms ...Match) Match {
	return func(v jsonx.Value) bool {
		for _, m := range ms {
			if m(v) {
				return true
			}
		}
		return false
	}
}

// Always 恒真条件。
func Always() Match { return func(jsonx.Value) bool { return true } }

// Never 恒假条件。
func Never() Match { return func(jsonx.Value) bool { return false } }

// CompileMatch 把 where 树编译为 Match 闭包，判定与 MatchesWhere 逐条等价；
// 由于 where 树在一次请求内不变，编译一次后可避免逐记录重复遍历与
// knownOperators/Keys 的分配（10 万条记录时的主要热点，纯性能修复）。
func CompileMatch(where *jsonx.Object) Match {
	if where == nil {
		return Always()
	}
	var ms []Match
	for _, key := range where.Keys() {
		value, _ := where.Get(key)
		if key == "or" {
			arr, isArr := jsonx.AsArray(value)
			if !isArr || len(arr) == 0 {
				return Never() // value 是静态 JSON：非数组或空数组对全部记录恒假
			}
			var subs []Match
			for _, sub := range arr {
				if subObj, isObj := asPlainObject(sub); isObj {
					subs = append(subs, CompileMatch(subObj))
				}
			}
			// 非对象子项跳过；全被跳过时 AnyOf() 恒假，与 matched=false 一致。
			ms = append(ms, AnyOf(subs...))
			continue
		}
		if key == "and" {
			// _where 支持 {"and": [...]} 取全部满足，与 "or" 同构
			// （v1 beta 把 "and" 当普通字段、值标量恒假；这里按 v0.17 风格作为
			// 保留键）。
			arr, isArr := jsonx.AsArray(value)
			if !isArr {
				return Never() // 非数组：恒假（与 "or" 的非数组处理镜像）
			}
			var subs []Match
			for _, sub := range arr {
				subObj, isObj := asPlainObject(sub)
				if !isObj {
					subs = append(subs, Never()) // 非对象子项无法满足
					continue
				}
				subs = append(subs, CompileMatch(subObj))
			}
			ms = append(ms, AllOf(subs...)) // 空数组：AllOf() 恒真（全部满足）
			continue
		}
		if sub, isObj := asPlainObject(value); isObj {
			if ops := knownOperators(sub); len(ops) > 0 {
				ops := compileOps(sub, ops)
				key := key
				ms = append(ms, func(rec jsonx.Value) bool {
					field, has := getField(rec, key)
					if !has {
						return false
					}
					for _, fn := range ops {
						if !fn(field) {
							return false
						}
					}
					return true
				})
				continue
			}
			subMatch := CompileMatch(sub)
			key := key
			ms = append(ms, func(rec jsonx.Value) bool {
				field, _ := getField(rec, key)
				if fieldObj, ok := asPlainObject(field); ok {
					return subMatch(fieldObj)
				}
				// 嵌套路径的中间字段不是对象（缺失、null 或标量）：子树里有运算符判定时
				// 不匹配（含 ne）；v1 在此放行是 bug，0.17 无此行为。子树无运算符
				// （如 {"meta":{"tags":{}}}）保持 v1 的放行语义。
				return !deepHasOperators(sub)
			})
			continue
		}
		return Never() // 标量或数组 value：源码 lines 82-84 恒 false
	}
	return AllOf(ms...)
}

// compileOps 按 operatorOrder 顺序把绑定值编译为判定闭包（数值/字符串边界预计算）。
func compileOps(sub *jsonx.Object, ops []string) []func(jsonx.Value) bool {
	var out []func(jsonx.Value) bool
	for _, op := range operatorOrder {
		bound, ok := sub.Get(op)
		if !ok {
			continue
		}
		out = append(out, compileBoundOp(op, bound))
	}
	return out
}

// compileBoundOp 单运算符编译：字符串/数字边界预计算，其余回退 applyWhereOp（语义一致）。
func compileBoundOp(op string, bound jsonx.Value) func(jsonx.Value) bool {
	switch op {
	case "eq", "ne":
		eqFn := compileEqNe(bound)
		if op == "eq" {
			return eqFn
		}
		return func(f jsonx.Value) bool { return !eqFn(f) }
	case "lt", "lte", "gt", "gte":
		cmpFn := compileRel(bound)
		switch op {
		case "lt":
			return func(f jsonx.Value) bool { c, ok := cmpFn(f); return ok && c < 0 }
		case "lte":
			return func(f jsonx.Value) bool { c, ok := cmpFn(f); return ok && c <= 0 }
		case "gt":
			return func(f jsonx.Value) bool { c, ok := cmpFn(f); return ok && c > 0 }
		default:
			return func(f jsonx.Value) bool { c, ok := cmpFn(f); return ok && c >= 0 }
		}
	case "in":
		arr, isArr := jsonx.AsArray(bound)
		if !isArr {
			arr = []jsonx.Value{bound}
		}
		if len(arr) == 0 {
			return Never()
		}
		return func(f jsonx.Value) bool {
			for _, e := range arr {
				if compileEqNe(e)(f) {
					return true
				}
			}
			return false
		}
	case "contains", "startsWith", "endsWith":
		lower := strings.ToLower(jsToString(bound))
		switch op {
		case "contains":
			return func(f jsonx.Value) bool {
				s, ok := f.(string)
				return ok && strings.Contains(strings.ToLower(s), lower)
			}
		case "startsWith":
			return func(f jsonx.Value) bool {
				s, ok := f.(string)
				return ok && strings.HasPrefix(strings.ToLower(s), lower)
			}
		default:
			return func(f jsonx.Value) bool {
				s, ok := f.(string)
				return ok && strings.HasSuffix(strings.ToLower(s), lower)
			}
		}
	}
	return func(f jsonx.Value) bool { return applyWhereOp(f, op, bound) }
}

// LooseBound 让 URL 里的值能匹配字符串字段：
// URL 参数没有类型信息，CoerceParamValue（v1 行为）之后保留参数原串，
// 使 eq/in/ne 可表达「按类型相等，或字段值转字符串后与参数原串完全相等」。
// 仅 URL 参数树中出现；_where 由 JSON 解析、类型明确，不使用该形态。
type LooseBound struct {
	Val jsonx.Value // CoerceParamValue 结果：按类型比较时即为 v1 语义
	Raw string      // 参数原串：字符串比较按原文、区分大小写
}

// looseEq 判定 URL 值是否相等（任一成立即相等）：
//   - jsStrictEq(field, Val)：按类型相等（v1 行为，含大小写敏感）；
//   - 字段为原始值且转字符串后与 Raw 完全相等：字符串 id/外键场景；
//   - 字段为数组时任一元素相等即相等（元素满足前两条）——数组包含，
//     比 0.17（数组转 "a,b" 串比较）宽松，属有意差异。
func looseEq(field jsonx.Value, b LooseBound) bool {
	if jsStrictEq(field, b.Val) {
		return true
	}
	switch f := field.(type) {
	case nil, bool, json.Number, string:
		// jsToString 为 JS String() 语义（null→"null"），不用 jsonx.ToString
		// （后者对 nil 返回空串），保证 null 字段不会匹配空串参数。
		if jsToString(f) == b.Raw {
			return true
		}
	case []jsonx.Value:
		for _, e := range f {
			if jsStrictEq(e, b.Val) {
				return true
			}
			switch e.(type) {
			case nil, bool, json.Number, string:
				if jsToString(e) == b.Raw {
					return true
				}
			}
		}
	}
	return false
}

// compileEqNe 编译 === 判定：字符串/数字/布尔/null 边界预计算（与 jsStrictEq 一致：
// 对象/数组之间恒 false）。
func compileEqNe(bound jsonx.Value) func(jsonx.Value) bool {
	switch bv := bound.(type) {
	case LooseBound:
		return func(f jsonx.Value) bool { return looseEq(f, bv) }
	case nil:
		return func(f jsonx.Value) bool { return f == nil }
	case bool:
		return func(f jsonx.Value) bool { b, ok := f.(bool); return ok && b == bv }
	case string:
		return func(f jsonx.Value) bool { s, ok := f.(string); return ok && s == bv }
	case json.Number:
		bn, berr := bv.Float64()
		return func(f jsonx.Value) bool {
			n, ok := f.(json.Number)
			if !ok {
				return false
			}
			fn, ferr := n.Float64()
			if ferr == nil && berr == nil {
				return fn == bn
			}
			return n.String() == bv.String() // 回退字面比较（与 jsStrictEq 相同）
		}
	}
	return func(f jsonx.Value) bool { return jsStrictEq(f, bound) }
}

// compileRel 编译关系比较：与 jsRelCmp 一致（双方字符串按字符串，否则双方 ToNumber）。
func compileRel(bound jsonx.Value) func(jsonx.Value) (int, bool) {
	bs, bIsStr := bound.(string)
	if bIsStr {
		// 双方字符串才按字符串比较；否则双方 ToNumber（bound 的 ToNumber 预计算）。
		bn, bOK := jsToNumber(bound)
		return func(f jsonx.Value) (int, bool) {
			if fs, ok := f.(string); ok {
				return strings.Compare(fs, bs), true
			}
			an, aOK := jsToNumber(f)
			if !aOK || !bOK {
				return 0, false
			}
			return relNumCmp(an, bn), true
		}
	}
	bn, bOK := jsToNumber(bound)
	return func(f jsonx.Value) (int, bool) {
		if _, ok := f.(string); ok {
			// 一边字符串一边数字：双方 ToNumber（JS "5" < 10 为 true）。
			an, aOK := jsToNumber(f)
			if !aOK || !bOK {
				return 0, false
			}
			return relNumCmp(an, bn), true
		}
		an, aOK := jsToNumber(f)
		if !aOK || !bOK {
			return 0, false
		}
		return relNumCmp(an, bn), true
	}
}

// relNumCmp 数字间比较。
func relNumCmp(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// operatorOrder 是 v1 where-operators.js WHERE_OPERATORS 的表内容与顺序（大小写敏感）。
var operatorOrder = []string{
	"lt", "lte", "gt", "gte", "eq", "ne", "in", "contains", "startsWith", "endsWith",
}

var operatorSet = map[string]struct{}{}

func init() {
	for _, op := range operatorOrder {
		operatorSet[op] = struct{}{}
	}
}

// IsOperator 报告给定名称是否为 v1 查询运算符；表匹配是大小写敏感的
// （v1 isWhereOperator 用 Array.includes 精确比较）。
func IsOperator(op string) bool {
	_, ok := operatorSet[op]
	return ok
}

// asPlainObject 对应 v1 的 isJSONObject：普通对象（不含数组）。
func asPlainObject(v jsonx.Value) (*jsonx.Object, bool) { return jsonx.AsObject(v) }

// getField 对应 matches-where 的 obj[key]：单段键取值；记录不是对象或键不存在时
// 对应 JS 的 undefined（ok=false）。
func getField(v jsonx.Value, key string) (jsonx.Value, bool) {
	obj, ok := jsonx.AsObject(v)
	if !ok {
		return nil, false
	}
	return obj.Get(key)
}

// MatchesWhere 忠实移植 v1 lib/matches-where.js：
//   - "or": [...] 任一子对象匹配；非数组或空数组恒假；非对象子项跳过。
//   - "and": [...]（保留键）全部子对象满足；非数组恒假；非对象子项恒假；空数组恒真。
//   - 值为含已知运算符的普通对象：字段缺失即恒假；其余运算符全部通过才真。
//   - 值为不含已知运算符的普通对象：字段为对象时递归匹配，否则该条件通过。
//   - 值为标量或数组：恒假。
func MatchesWhere(v jsonx.Value, where *jsonx.Object) bool {
	for _, key := range where.Keys() {
		value, _ := where.Get(key)
		if key == "or" {
			arr, isArr := jsonx.AsArray(value)
			if !isArr || len(arr) == 0 {
				return false
			}
			matched := false
			for _, sub := range arr {
				subObj, isObj := asPlainObject(sub)
				if isObj && MatchesWhere(v, subObj) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
			continue
		}
		if key == "and" {
			// 保留键：全部子 where 满足才真（判定与 CompileMatch 的 "and" 等价）。
			arr, isArr := jsonx.AsArray(value)
			if !isArr {
				return false
			}
			for _, sub := range arr {
				subObj, isObj := asPlainObject(sub)
				if !isObj || !MatchesWhere(v, subObj) {
					return false
				}
			}
			continue
		}
		field, hasField := getField(v, key)
		if sub, isObj := asPlainObject(value); isObj {
			if len(knownOperators(sub)) > 0 {
				if !hasField {
					return false
				}
				for _, op := range operatorOrder {
					opv, ok := sub.Get(op)
					if !ok {
						continue
					}
					if !applyWhereOp(field, op, opv) {
						return false
					}
				}
				continue
			}
			fieldObj, ok := asPlainObject(field)
			if !ok {
				// 中间字段不是对象（缺失、null 或标量）且子树含运算符 → 不匹配，
				// 与 CompileMatch 等价；无运算符子树保持 v1 的放行语义。
				if deepHasOperators(sub) {
					return false
				}
				continue
			}
			if !MatchesWhere(fieldObj, sub) {
				return false
			}
			continue
		}
		// 标量或数组 value：源码 lines 82-84 恒 return false。
		return false
	}
	return true
}

// deepHasOperators 递归判断 where 子树里是否存在「含运算符的判定对象」。
// 用来区分：中间字段缺失时，有运算符的子树必须排除（包括 ne），
// 纯字段名嵌套而无运算符的（{"meta":{"tags":{}}}）
// 保持 v1 的放行语义（TestMatchesWhere）。
func deepHasOperators(o *jsonx.Object) bool {
	for _, k := range o.Keys() {
		if k == "or" || k == "and" {
			return true // 保留键子树本身有判定语义
		}
		v, _ := o.Get(k)
		if sub, isObj := asPlainObject(v); isObj {
			if len(knownOperators(sub)) > 0 || deepHasOperators(sub) {
				return true
			}
		}
	}
	return false
}

// knownOperators 对应 getKnownOperators：按表中顺序收集对象里出现的已知运算符键。
func knownOperators(o *jsonx.Object) []string {
	var out []string
	for _, op := range operatorOrder {
		if _, ok := o.Get(op); ok {
			out = append(out, op)
		}
	}
	return out
}

// applyWhereOp 单个运算符判定（均为 JS 严格/关系语义）；未知运算符名不参与（由调用方过滤）。
func applyWhereOp(field jsonx.Value, op string, bound jsonx.Value) bool {
	switch op {
	case "lt":
		c, ok := jsRelCmp(field, bound)
		return ok && c < 0
	case "lte":
		c, ok := jsRelCmp(field, bound)
		return ok && c <= 0
	case "gt":
		c, ok := jsRelCmp(field, bound)
		return ok && c > 0
	case "gte":
		c, ok := jsRelCmp(field, bound)
		return ok && c >= 0
	case "eq":
		return jsStrictEq(field, bound)
	case "ne":
		return !jsStrictEq(field, bound)
	case "in":
		arr, isArr := jsonx.AsArray(bound)
		if !isArr {
			arr = []jsonx.Value{bound}
		}
		for _, e := range arr {
			if jsStrictEq(field, e) {
				return true
			}
		}
		return false
	case "contains":
		s, isStr := field.(string)
		if !isStr {
			return false
		}
		return strings.Contains(strings.ToLower(s), strings.ToLower(jsToString(bound)))
	case "startsWith":
		s, isStr := field.(string)
		if !isStr {
			return false
		}
		return strings.HasPrefix(strings.ToLower(s), strings.ToLower(jsToString(bound)))
	case "endsWith":
		s, isStr := field.(string)
		if !isStr {
			return false
		}
		return strings.HasSuffix(strings.ToLower(s), strings.ToLower(jsToString(bound)))
	}
	return true
}

// CompileWhere 把 _where JSON 对象编译为 Match；error 恒 nil（保留签名兼容）。
func CompileWhere(obj *jsonx.Object) (Match, error) {
	return CompileMatch(obj), nil
}

// CompileWhereValue 编译已解析的 _where 值（v1 app.js parseListParams）：
// 普通对象 → MatchesWhere 语义；数组 → Object.entries 语义（无键则恒真，否则标量值恒假）；
// 其余调用方应忽略回退普通参数。
func CompileWhereValue(v jsonx.Value) (Match, bool) {
	if obj, ok := asPlainObject(v); ok {
		return CompileMatch(obj), true
	}
	if arr, ok := jsonx.AsArray(v); ok {
		if len(arr) == 0 {
			return Always(), true
		}
		return Never(), true
	}
	return nil, false
}

// --- JS 语义助手 ---

// jsStrictEq 对应 ===：原始值按类型与值比较；对象/数组引用恒不等。
func jsStrictEq(a, b jsonx.Value) bool {
	if lb, ok := b.(LooseBound); ok {
		return looseEq(a, lb)
	}
	switch av := a.(type) {
	case nil:
		return b == nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok {
			return false
		}
		af, aerr := av.Float64()
		bf, berr := bv.Float64()
		if aerr == nil && berr == nil {
			return af == bf
		}
		return av.String() == bv.String()
	case string:
		bs, ok := b.(string)
		return ok && av == bs
	}
	return false
}

// jsRelCmp 对应 JS 关系比较（< <= > >= 的 ToPrimitive/ToNumber 语义）：
// 双方均为字符串时按字符串（码位序）比较，否则双方 ToNumber；
// 任一为 NaN 时 ok=false（所有关系比较都是 false）。
func jsRelCmp(a, b jsonx.Value) (int, bool) {
	as, aIsStr := a.(string)
	bs, bIsStr := b.(string)
	if aIsStr && bIsStr {
		return strings.Compare(as, bs), true
	}
	an, aOK := jsToNumber(a)
	bn, bOK := jsToNumber(b)
	if !aOK || !bOK {
		return 0, false
	}
	switch {
	case an < bn:
		return -1, true
	case an > bn:
		return 1, true
	}
	return 0, true
}

// jsToNumber 对应 ToNumber（对象为 NaN；原始值见实现）。ok=false 表示 NaN。
func jsToNumber(v jsonx.Value) (float64, bool) {
	switch tv := v.(type) {
	case nil:
		return 0, true
	case bool:
		if tv {
			return 1, true
		}
		return 0, true
	case json.Number:
		f, err := tv.Float64()
		return f, err == nil
	case string:
		return jsNumberFromString(tv)
	}
	return 0, false
}

var decimalRe = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// jsNumberFromString 对应 JS Number(字符串)：空/纯空白为 0；支持 0x 十六进制、
// Infinity；其余可解析为有限十进制数；否则 NaN（ok=false）。
func jsNumberFromString(s string) (float64, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, true
	}
	if len(t) > 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
		u, err := strconv.ParseInt(t[2:], 16, 64)
		if err != nil {
			return 0, false
		}
		return float64(u), true
	}
	if t == "Infinity" || t == "+Infinity" {
		return math.Inf(1), true
	}
	if t == "-Infinity" {
		return math.Inf(-1), true
	}
	if decimalRe.MatchString(t) {
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

// jsNumberString 对应 String(number)：文件里的 1.50、1000.0 比较时是 1.5、1000。
// 纯整数字面量保留原文，这样超出双精度范围的大整数仍能按原文匹配。
func jsNumberString(n json.Number) string {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		return s
	}
	f, err := n.Float64()
	if err != nil {
		return s
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// jsToString 对应 .toString()/String(x)：null→"null"、布尔、数字（JS 形式）、字符串；
// 对象→"[object Object]"；数组按 join(",")（null 元素为空串）。
func jsToString(v jsonx.Value) string {
	switch tv := v.(type) {
	case nil:
		return "null"
	case bool:
		if tv {
			return "true"
		}
		return "false"
	case json.Number:
		return jsNumberString(tv)
	case string:
		return tv
	case []jsonx.Value:
		parts := make([]string, len(tv))
		for i, e := range tv {
			if _, isArr := e.([]jsonx.Value); isArr {
				parts[i] = jsToString(e)
			} else if e == nil {
				parts[i] = ""
			} else {
				parts[i] = jsToString(e)
			}
		}
		return strings.Join(parts, ",")
	case *jsonx.Object:
		return "[object Object]"
	}
	return ""
}

// jsTruthy 对应 JS 真值判断。
func jsTruthy(v jsonx.Value) bool {
	switch tv := v.(type) {
	case nil:
		return false
	case bool:
		return tv
	case json.Number:
		f, err := tv.Float64()
		if err != nil {
			return true
		}
		return f != 0
	case string:
		return tv != ""
	}
	return true
}

// --- v1 parse-where（lib/parse-where.js 移植） ---

// SplitV1Key 把查询键拆为 (path, op)。ok=false 表示整个参数应丢弃
// （冒号后是未知运算符时 v1 返回 {path, op:null}）。
func SplitV1Key(key string) (path, op string, ok bool) {
	if i := strings.LastIndex(key, ":"); i != -1 {
		p, o := key[:i], key[i+1:]
		if o == "" {
			return key, "eq", true
		}
		if IsOperator(o) {
			return p, o, true
		}
		return p, "", false
	}
	// v0.17 风格兼容：^(.*)_([a-z]+)$ 且末段是已知运算符。
	if m := underscoreOpRe.FindStringSubmatch(key); m != nil && m[1] != "" && IsOperator(m[2]) {
		return m[1], m[2], true
	}
	return key, "eq", true
}

var underscoreOpRe = regexp.MustCompile(`^(.*)_([a-z]+)$`)

// CoerceParamValue 对应 parse-where 的 coerceValue：
// "true"/"false"/"null" 转真类型；trim 后为空返回原串；Number() 有限则转数字；否则原串。
func CoerceParamValue(s string) jsonx.Value {
	switch s {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if strings.TrimSpace(s) == "" {
		return s
	}
	// v1 用 Number.isFinite 判定：Infinity/NaN 保持原串。
	if f, ok := jsNumberFromString(s); ok && !math.IsInf(f, 0) && !math.IsNaN(f) {
		return json.Number(formatJSONNumber(f))
	}
	return s
}

// formatJSONNumber 把 JS Number 结果格式化为 json.Number 文本（整值去小数点）。
func formatJSONNumber(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// SetParamToWhere 对应 setPathOp：把单个查询参数写入 where 树（点号路径嵌套）。
// op=="in" 时值按逗号拆分为数组并逐项 trim+coerce（v1 语义）。
func SetParamToWhere(root *jsonx.Object, path, op, rawValue string) {
	parts := strings.Split(path, ".")
	if op == "in" {
		segs := strings.Split(rawValue, ",")
		vals := make([]jsonx.Value, len(segs))
		for i, seg := range segs {
			trimmed := strings.TrimSpace(seg)
			// in 的每个元素同样携带类型值与原串两个形态。
			vals[i] = jsonx.Value(LooseBound{Val: CoerceParamValue(trimmed), Raw: trimmed})
		}
		setNestedOp(root, parts, op, jsonx.Value(vals))
		return
	}
	switch op {
	case "eq", "ne":
		// URL 参数无类型信息，eq/ne 用 LooseBound 双形态。
		setNestedOp(root, parts, op, LooseBound{Val: CoerceParamValue(rawValue), Raw: rawValue})
	default:
		setNestedOp(root, parts, op, CoerceParamValue(rawValue))
	}
}

// setNestedOp 沿 dot-prop 语义写入 path.op；路径上非对象节点会被替换为新对象。
func setNestedOp(root *jsonx.Object, parts []string, op string, val jsonx.Value) {
	cur := root
	for _, p := range parts {
		next, ok := cur.Get(p)
		nobj, isObj := asPlainObject(next)
		if !ok || !isObj {
			nobj = jsonx.NewObject()
			cur.Set(p, nobj)
		}
		cur = nobj
	}
	cur.Set(op, val)
}

// GetPathParts 沿已拆分的点号路径取值（v1 dot-prop getProperty 语义）。
func GetPathParts(v jsonx.Value, parts []string) (jsonx.Value, bool) {
	cur := v
	for _, part := range parts {
		obj, ok := asPlainObject(cur)
		if !ok {
			return nil, false
		}
		val, ok := obj.Get(part)
		if !ok {
			return nil, false
		}
		cur = val
	}
	return cur, true
}

// --- v0.17 legacy 过滤（lib/server/router/plural.js lines 59-159） ---

// LegacyFieldMatch 实现 0.17 单个查询键的过滤：键可带 _ne/_lte/_gte/_like 后缀；
// values 为该键的多值——除 _ne 用 AND 归约外其余用 OR 归约；
// 字段缺失或为 null 时所有运算符都不匹配（源码先 return undefined）。
//
// _like 的正则在编译期（每请求一次）编译，逐条记录判定时不再重复编译。
func LegacyFieldMatch(key string, values []string) Match {
	isDifferent := strings.HasSuffix(key, "_ne")
	isRange := strings.HasSuffix(key, "_lte") || strings.HasSuffix(key, "_gte")
	isLike := strings.HasSuffix(key, "_like")
	isLowerThan := strings.HasSuffix(key, "_gte")
	path := legacyStripRe.ReplaceAllString(key, "")
	parts := lodashPathParts(path)
	// 编译期把每个 _like 模式编成正则（对应原版每请求 new RegExp 的次数）。
	var res []*regexp.Regexp
	if isLike {
		for _, v := range values {
			res = append(res, compileLikeRe(v))
		}
	}
	return func(v jsonx.Value) bool {
		ev, _ := lodashGet(v, parts)
		if ev == nil {
			return false
		}
		var result bool
		for i, val := range values {
			var re *regexp.Regexp
			if isLike {
				re = res[i]
			}
			r := legacyOneMatch(isRange, isLowerThan, isDifferent, isLike, re, val, ev)
			if i == 0 {
				result = r
			} else if isDifferent {
				result = result && r
			} else {
				result = result || r
			}
		}
		return result
	}
}

var legacyStripRe = regexp.MustCompile(`(_lte|_gte|_ne|_like)$`)

// legacyOneMatch 单值单运算符判定（0.17 plural.js 的 map 回调）。
func legacyOneMatch(isRange, isLowerThan, isDifferent, isLike bool, re *regexp.Regexp, param string, ev jsonx.Value) bool {
	if isRange {
		c, ok := jsRelCmpParam(param, ev)
		if !ok {
			return false
		}
		if isLowerThan {
			// _gte：value <= elementValue
			return c <= 0
		}
		// _lte：value >= elementValue
		return c >= 0
	}
	if isDifferent {
		return param != jsToString(ev)
	}
	if isLike {
		if re == nil {
			return false // 两级模式都编译失败：永不命中（见 compileLikeRe）
		}
		return re.MatchString(jsToString(ev))
	}
	return param == jsToString(ev)
}

// jsRelCmpParam 查询参数（字符串）与元素原始值的关系比较：双方字符串时按字符串，
// 否则双方 ToNumber；NaN 时 ok=false。
func jsRelCmpParam(param string, ev jsonx.Value) (int, bool) {
	if es, ok := ev.(string); ok {
		return strings.Compare(param, es), true
	}
	pn, pOK := jsNumberFromString(param)
	if !pOK {
		return 0, false
	}
	en, eOK := jsToNumber(ev)
	if !eOK {
		return 0, false
	}
	switch {
	case pn < en:
		return -1, true
	case pn > en:
		return 1, true
	}
	return 0, true
}

// compileLikeRe 对应 new RegExp(pattern, "i")；模式非法时退化为字面量匹配
// （原版会抛异常导致 500，此处仅在极少数非法 JS 专有语法下退化）。
// 每请求（每查询键）编译一次，逐记录复用。两级模式都编译失败时返回 nil，
// 调用方按"永不命中"处理。
func compileLikeRe(pattern string) *regexp.Regexp {
	re, err := regexp.Compile(`(?i)` + pattern)
	if err != nil {
		re, err = regexp.Compile(`(?i)` + regexp.QuoteMeta(pattern))
		if err != nil {
			return nil
		}
	}
	return re
}

// lodashPathParts 按 lodash getPath 的简化语义拆分路径：
// 不含 . [ ] 时整串是单个键；否则按 . 与 [..] 分隔（[ 内引号被剥离）。
func lodashPathParts(path string) []string {
	if !strings.ContainsAny(path, ".[") {
		return []string{path}
	}
	var out []string
	var cur strings.Builder
	flush := func() { out = append(out, cur.String()); cur.Reset() }
	i := 0
	for i < len(path) {
		switch path[i] {
		case '.':
			flush()
			i++
		case '[':
			if cur.Len() > 0 {
				flush()
			}
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				cur.WriteString(path[i:])
				i = len(path)
				continue
			}
			seg := path[i+1 : i+end]
			seg = strings.Trim(seg, `"'`)
			out = append(out, seg)
			i += end + 1
			if i < len(path) && path[i] == '.' {
				i++
			}
		default:
			cur.WriteByte(path[i])
			i++
		}
	}
	flush()
	return out
}

// lodashGet 沿路径取值：对象按键、数组按十进制下标；缺失返回 (nil,false)。
func lodashGet(v jsonx.Value, parts []string) (jsonx.Value, bool) {
	cur := v
	for _, p := range parts {
		switch cv := cur.(type) {
		case *jsonx.Object:
			val, ok := cv.Get(p)
			if !ok {
				return nil, false
			}
			cur = val
		case []jsonx.Value:
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 0 || idx >= len(cv) {
				return nil, false
			}
			cur = cv[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// lodashHasAny 对应 0.17 的"自动删除查询参数"检查：
// 任一记录里存在该（路径）键时返回 true（_.has 语义：键存在即算，null 值也算）。
func lodashHasAny(records []jsonx.Value, key string) bool {
	parts := lodashPathParts(key)
	for _, rec := range records {
		if lodashHas(rec, parts) {
			return true
		}
	}
	return false
}

func lodashHas(v jsonx.Value, parts []string) bool {
	cur := v
	for _, p := range parts {
		switch cv := cur.(type) {
		case *jsonx.Object:
			val, ok := cv.Get(p)
			if !ok {
				return false
			}
			cur = val
		case []jsonx.Value:
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 0 || idx >= len(cv) {
				return false
			}
			cur = cv[idx]
		default:
			return false
		}
	}
	return true
}

// LegacyHasParam 报告任一记录是否含该键路径（0.17 自动删除参数用）。
func LegacyHasParam(records []jsonx.Value, key string) bool {
	return lodashHasAny(records, key)
}

// --- 全文匹配 ---

// FullTextLegacy 是 v0.17 的 q 参数全文匹配（mixins.js deepQuery）：
// q 已由调用方小写化；值为假时跳过（value && q），字符串/toString 结果做包含匹配。
func FullTextLegacy(q string) Match {
	return func(v jsonx.Value) bool {
		return deepQueryMatch(v, q)
	}
}

func deepQueryMatch(v jsonx.Value, q string) bool {
	if q == "" || !jsTruthy(v) {
		return false
	}
	switch tv := v.(type) {
	case []jsonx.Value:
		for _, e := range tv {
			if deepQueryMatch(e, q) {
				return true
			}
		}
	case *jsonx.Object:
		for _, k := range tv.Keys() {
			if deepQueryMatch(tv.MustGet(k), q) {
				return true
			}
		}
	default:
		return strings.Contains(strings.ToLower(jsToString(v)), q)
	}
	return false
}
