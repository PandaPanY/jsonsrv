package httpapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
	"github.com/PandaPanY/jsonsrv/internal/query"
)

// queryMany 处理 GET /资源：条件过滤 → 排序 → 嵌入 → 分页/切片。
// extra 是路由层注入的附加条件（如嵌套路由的 postId=1）。
func (a *App) queryMany(w http.ResponseWriter, r *http.Request, resID string, res resourceInfo, extra ...query.Match) {
	all := res.asArray()
	match, err := a.buildMatch(r, all)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(extra) > 0 {
		match = query.AllOf(append([]query.Match{match}, extra...)...)
	}
	items := filterRecords(all, match)
	q := r.URL.Query()
	if spec := q.Get("_sort"); spec != "" {
		query.SortWithOrder(items, spec, q.Get("_order"), a.opt.Legacy)
	}
	// 先按参数取分页/切片窗口并写好响应头，再只对窗口内记录做嵌入
	// （输出与"嵌入后窗口化"逐字段一致，嵌入成本从全量降到页大小）。
	if window, writeBody, ok := a.windowForResponse(w, r, items); ok {
		writeBody(w, a.applyEmbeds(r, resID, window))
		return
	}
	items = a.applyEmbeds(r, resID, items)
	a.writeResult(w, r, resID, items)
}

// queryOne 处理 GET /资源/:id（支持 _embed/_expand）。
func (a *App) queryOne(w http.ResponseWriter, r *http.Request, resID string, res resourceInfo, idParam string) {
	idx := a.findRecord(res.asArray(), idParam)
	if idx < 0 {
		writeErr(w, http.StatusNotFound, "record not found")
		return
	}
	rec := res.asArray()[idx]
	if a.wantsEmbed(r) {
		rec = a.applyEmbeds(r, resID, []jsonx.Value{rec})[0]
	}
	writeJSON(w, http.StatusOK, rec)
}

// writeResult 按参数选择响应形态：
// --legacy 下 _page 返回数组并带 Link/X-Total-Count 头（v0.17），
// 以及 _start/_end/_limit 切片并带 X-Total-Count 头（0.17 plural.js 实测）。
// 默认（v1）模式：_page 返回 v1 包裹对象（_per_page 缺失时用 _limit 当页大小）；
// _start/_end/_limit 切片同样生效并发 X-Total-Count
// （v1 beta 把它们当过滤条件，不跟）。
func (a *App) writeResult(w http.ResponseWriter, r *http.Request, resID string, items []jsonx.Value) {
	q := r.URL.Query()
	if q.Has("_page") && a.opt.Legacy {
		writeLegacyPage(w, r, items)
		return
	}
	if q.Has("_page") {
		writePageEnvelope(w, r, items)
		return
	}
	// 0.17 切片：两种模式都生效并带 X-Total-Count。
	if sp, ok := parseSliceParams(q); ok {
		total := len(items)
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		// 0.17 plural.js：切片/分页时设 Access-Control-Expose-Headers，
		// 仅 _page 时追加 Link（分页分支在 writeLegacyPage 中设置）。
		w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
		items = sp.apply(items)
	}
	writeJSON(w, http.StatusOK, items)
}

// sliceParams 是 _start/_end/_limit 的解析结果。
// 0.17 语义：参数存在（非空串）即生效；数值按 lodash _.slice（即 Array.prototype.slice）
// 解释：负数从长度加上偏移，越界钳制；_end 为上界且排他（0.17 README：_start inclusive，_end exclusive）。
// X-Total-Count 为筛选后的总条数（0.17 plural.js 在切片前取 chain.size()）。
type sliceParams struct {
	start string
	end   string
	limit string
}

// parseSliceParams 解析 _start/_end/_limit（_end 或 _limit 任一存在即生效）。
// 仅 _start 存在时 0.17 不切片也不发响应头。
// 空值等同没有传该参数（0.17 源码为 JS falsy 判断：_start || _end || _limit）。
func parseSliceParams(q url.Values) (sliceParams, bool) {
	end, limit := q.Get("_end"), q.Get("_limit")
	if end == "" && limit == "" {
		return sliceParams{}, false
	}
	return sliceParams{start: q.Get("_start"), end: end, limit: limit}, true
}

// jsParseInt 按 JS parseInt(s, 10) 语义解析（0.17 显式传基数 10）：去前导空白与
// 符号后，取前面的十进制数字截断，解析不出数字返回 ok=false（等价 NaN）。
// 例："2abc"→2、"1.5"→1、"0x2"→0、"abc"→NaN、"-1"→-1、""→NaN。
func jsParseInt(s string) (int64, bool) {
	t := strings.TrimLeft(s, " \t\r\n\v\f")
	if t == "" {
		return 0, false
	}
	neg := false
	if t[0] == '+' || t[0] == '-' {
		neg = t[0] == '-'
		t = t[1:]
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(t[:i], 10, 64)
	if err != nil {
		return 0, false // 溢出按 NaN 处理（对切片而言越界）
	}
	if neg {
		v = -v
	}
	return v, true
}

// clampIndex 按 parseInt 解析后套用 Array.slice 的负数/越界钳制（负数加长度，上界钳到长度）。
func clampIndex(s string, n int) int {
	v, ok := jsParseInt(s)
	if !ok {
		return 0
	}
	if v < 0 {
		v += int64(n)
		if v < 0 {
			return 0
		}
	}
	if v > int64(n) {
		return n
	}
	return int(v)
}

// apply 按 v0.17 切片：_end 存在时 chain.slice(_start,_end)；
// 否则 _limit 存在时 chain.slice(_start,_start+_limit)。
// 数值按 JS parseInt 解析（1.5→1、2abc→2、abc→NaN 得空切片）；
// _limit 为负等价于负的结束下标，lodash baseSlice 会给负 end 加长度
// （chain.slice(0,-1) 即尾部裁一格的「全部减最后」，不是空切片）。
func (sp sliceParams) apply(items []jsonx.Value) []jsonx.Value {
	n := len(items)
	start := clampIndex(sp.start, n)
	if sp.limit == "" {
		end := clampIndex(sp.end, n)
		return sliceRange(items, start, end)
	}
	lim, ok := jsParseInt(sp.limit)
	if !ok {
		return sliceRange(items, start, start) // NaN → 空切片
	}
	if lim >= int64(n) {
		return sliceRange(items, start, n)
	}
	stop := start + int(lim)
	if stop < 0 {
		stop += n // 负 end：加长度（与 Array.slice 一致）
		if stop < 0 {
			stop = 0
		}
	}
	if stop > n {
		stop = n
	}
	return sliceRange(items, start, stop)
}

func sliceRange(items []jsonx.Value, start, stop int) []jsonx.Value {
	if stop < start {
		stop = start
	}
	if start > len(items) {
		start = len(items)
	}
	return items[start:stop]
}

// writeLegacyPage 实现 v0.17 分页：返回数组，带 Link（first/prev/next/last）与 X-Total-Count 头。
// 页大小取 _limit（默认 10），_page 从 1 开始，非法或越界自动修正。
func writeLegacyPage(w http.ResponseWriter, r *http.Request, items []jsonx.Value) {
	start, end := legacyPageHeaders(w, r, items)
	writeJSON(w, http.StatusOK, items[start:end])
}

// legacyPageHeaders 写出 0.17 分页的全部响应头，返回当前页窗口 [start,end)。
// 0.17 utils.getPage 实测语义：切出空页时不产生任何链接；结果即全量（单页）时
// 也不产生链接；Link 头始终输出（无链接时为空值，与 express res.links() 一致）。
func legacyPageHeaders(w http.ResponseWriter, r *http.Request, items []jsonx.Value) (start, end int) {
	q := r.URL.Query()
	per, _ := strconv.Atoi(q.Get("_limit"))
	if per <= 0 {
		per = 10
	}
	page, _ := strconv.Atoi(q.Get("_page"))
	if page < 1 {
		page = 1
	}
	total := len(items)
	pages, start, end := pageWindow(page, per, total) // 与 v1 分页共用同一份计算

	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	// 0.17 plural.js：_page 时 Expose 追加 Link，前端才能读到分页头。
	w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count, Link")
	var link string
	if end > start && end-start != total {
		link = legacyLink(r, page, pages)
	}
	w.Header().Set("Link", link)
	return start, end
}

// windowForResponse 是窗口化路径：先按参数取分页/切片窗口、写好响应头，
// 返回窗口和"用窗口记录组装正文"的函数，让 _embed 只作用于窗口内的记录；
// ok=false 表示请求不带分页/切片参数，由调用方按全量路径处理。
func (a *App) windowForResponse(w http.ResponseWriter, r *http.Request, items []jsonx.Value) ([]jsonx.Value, func(http.ResponseWriter, []jsonx.Value), bool) {
	q := r.URL.Query()
	if q.Has("_page") {
		if a.opt.Legacy {
			start, end := legacyPageHeaders(w, r, items)
			return items[start:end], writeJSONArray, true
		}
		total := len(items)
		start, end, page, pages := v1PageBounds(q, total)
		return items[start:end], func(w http.ResponseWriter, recs []jsonx.Value) {
			writePageEnvelopeBody(w, page, pages, total, recs)
		}, true
	}
	// 0.17 切片：两种模式都走这条窗口化路径。
	if sp, ok := parseSliceParams(q); ok {
		total := len(items)
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		// 0.17 plural.js：切片/分页时设 Access-Control-Expose-Headers（仅 _page 时
		// 追加 Link，那在 legacyPageHeaders 中处理）。
		w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
		return sp.apply(items), writeJSONArray, true
	}
	return nil, nil, false
}

// writeJSONArray 写出普通数组正文。
func writeJSONArray(w http.ResponseWriter, recs []jsonx.Value) {
	writeJSON(w, http.StatusOK, recs)
}

// legacyLink 构造 v0.17 的 Link 头（first/prev/next/last）。
// 0.17 实测：URL 为"协议://请求 Host + 原始请求 URL"，仅把 page=当前页 的子串
// 替换为目标页（首个匹配），其余参数与顺序原样保留（get-full-url.js +
// fullURL.replace 语义）；页 1 无 prev，末页无 next。
func legacyLink(r *http.Request, page, pages int) string {
	base := requestURL(r)
	repl := func(p int) string {
		return strings.Replace(base, "page="+strconv.Itoa(page), "page="+strconv.Itoa(p), 1)
	}
	var parts []string
	add := func(p int, rel string) {
		parts = append(parts, fmt.Sprintf(`<%s>; rel="%s"`, repl(p), rel))
	}
	add(1, "first")
	if page > 1 {
		add(page-1, "prev")
	}
	if page < pages {
		add(page+1, "next")
	}
	add(pages, "last")
	return strings.Join(parts, ", ")
}

// writePageEnvelope 输出 v1 的 {first,prev,next,last,pages,items,data} 包裹对象。
// 对照版实测：first/prev/next/last 为页号而非 URL，无相邻页时为 null。
// 页码非法或越界时修正到合法范围，不报错。
func writePageEnvelope(w http.ResponseWriter, r *http.Request, items []jsonx.Value) {
	total := len(items)
	start, end, page, pages := v1PageBounds(r.URL.Query(), total)
	writePageEnvelopeBody(w, page, pages, total, items[start:end])
}

// v1PageBounds 计算 v1 包裹对象的窗口与页号（窗口先于嵌入确定）。
// v1 语义：页码非法或越界时修正到 [1,pages]。
// _per_page 缺失时用 _limit 当每页条数（0.17 的 parseInt(x)||10，非法按 10）；
// _per_page 存在时以其为准（v1 原有行为不变）。
func v1PageBounds(q url.Values, total int) (start, end, page, pages int) {
	per, _ := strconv.Atoi(q.Get("_per_page"))
	if per <= 0 {
		per, _ = strconv.Atoi(q.Get("_limit"))
	}
	if per <= 0 {
		per = 10
	}
	page, _ = strconv.Atoi(q.Get("_page"))
	pages, _, _ = pageWindow(1, per, total)
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	pages, start, end = pageWindow(page, per, total)
	return start, end, page, pages
}

// pageWindow 计算总页数与当前页窗口（0.17 与 v1 分页共用）。
// page 由调用方预先归一；page 越界时窗口自然为空（start==end==total）。
func pageWindow(page, per, total int) (pages, start, end int) {
	ppages := int(math.Ceil(float64(total) / float64(per)))
	if ppages == 0 {
		ppages = 1
	}
	start = (page - 1) * per
	if start > total {
		start = total
	}
	end = start + per
	if end > total {
		end = total
	}
	return ppages, start, end
}

// writePageEnvelopeBody 按页号与窗口记录写出 v1 包裹对象。
func writePageEnvelopeBody(w http.ResponseWriter, page, pages, total int, data []jsonx.Value) {
	// 对照 1.0.0-beta.15 实测：first/prev/next/last 是页号（无相邻页时为 null），
	// 不是 URL；pages/items 为数字。
	env := jsonx.NewObject()
	env.Set("first", json.Number(strconv.Itoa(1)))
	prev := jsonx.Value(nil)
	if page > 1 {
		prev = json.Number(strconv.Itoa(page - 1))
	}
	env.Set("prev", prev)
	next := jsonx.Value(nil)
	if page < pages {
		next = json.Number(strconv.Itoa(page + 1))
	}
	env.Set("next", next)
	env.Set("last", json.Number(strconv.Itoa(pages)))
	env.Set("pages", json.Number(strconv.Itoa(pages)))
	env.Set("items", json.Number(strconv.Itoa(total)))
	env.Set("data", data)
	writeJSON(w, http.StatusOK, env)
}

// filterRecords 按条件过滤，返回新切片。
// 大集合（≥8000 条）分块并行判定：Match 闭包是只读的（正则已在
// 编译期构建），并发执行安全；按输入顺序收集分块结果，输出顺序不变。
func filterRecords(arr []jsonx.Value, m query.Match) []jsonx.Value {
	if len(arr) < 8000 {
		out := make([]jsonx.Value, 0, len(arr))
		for _, rec := range arr {
			if m(rec) {
				out = append(out, rec)
			}
		}
		return out
	}
	out := filterRecordsParallel(arr, m)
	return out
}

// filterRecordsParallel 分块并行过滤并保持输入顺序。
func filterRecordsParallel(arr []jsonx.Value, m query.Match) []jsonx.Value {
	w := runtime.NumCPU()
	if w > 8 {
		w = 8
	}
	if w < 2 {
		w = 2
	}
	chunk := (len(arr) + w - 1) / w
	parts := make([][]jsonx.Value, (len(arr)+chunk-1)/chunk)
	var wg sync.WaitGroup
	for i := range parts {
		lo, hi := i*chunk, i*chunk+chunk
		if hi > len(arr) {
			hi = len(arr)
		}
		wg.Add(1)
		go func(i, lo, hi int) {
			defer wg.Done()
			sub := make([]jsonx.Value, 0, hi-lo)
			for _, rec := range arr[lo:hi] {
				if m(rec) {
					sub = append(sub, rec)
				}
			}
			parts[i] = sub
		}(i, lo, hi)
	}
	wg.Wait()
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	out := make([]jsonx.Value, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
