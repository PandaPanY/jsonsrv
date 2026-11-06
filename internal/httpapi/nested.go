package httpapi

import (
	"net/http"

	"github.com/PandaPanY/jsonsrv/internal/inflect"
	"github.com/PandaPanY/jsonsrv/internal/jsonx"
	"github.com/PandaPanY/jsonsrv/internal/query"
)

// handleNested 实现 v0.17 的嵌套路由（始终识别，不依赖 --legacy；v1 无嵌套路由，
// 属有意差异）：
//
//	GET  /posts/1/comments  ≡ GET /comments?postId=1（可再叠加查询参数）
//	POST /posts/1/comments  自动写入 postId（父记录存在时取其 id 原值，否则取路径原文；legacy 写字符串）
//
// 外键名 = 父资源名单数 + 外键后缀（默认 Id），与 --fks 配置一致。
// 两种模式都不检查父记录是否存在（0.17 nested.js 行为：
// 嵌套路由 ≡ 同名查询参数过滤，父缺失时 GET 得 200 空数组、POST 得 201）；
// GET 的外键按路径原文比较（≡ /products?vendorId=<路径段>），同名查询参数被路径值覆盖
// （0.17 nested.js：query[fk] = params.id）。
// 路径段不做类型转换：id 可能是 "007"、"12e4"、"0x10" 这样的字符串，转换后就对不上了。
func (a *App) handleNested(w http.ResponseWriter, r *http.Request, snap *jsonx.Object, resID string, res resourceInfo, rest []string) {
	if res.singular || len(rest) != 2 {
		writeErr(w, http.StatusNotFound, "unknown route")
		return
	}
	idParam, childName := rest[0], rest[1]

	childRes, ok := a.resource(snap, childName)
	if !ok || childRes.singular {
		writeErr(w, http.StatusNotFound, "no resource named "+childName)
		return
	}

	fk := inflect.Singular(resID) + a.opt.FKS

	switch r.Method {
	case http.MethodGet:
		// 0.17 nested.js：query[fk] = params.id 覆盖同名查询参数（路径外键优先，
		// 默认模式同样处理）。
		vs := r.URL.Query()
		if vs.Has(fk) {
			vs.Del(fk)
			r.URL.RawQuery = vs.Encode()
		}
		if a.opt.Legacy {
			a.queryMany(w, r, childName, childRes, query.LegacyFieldMatch(fk, []string{idParam}))
			return
		}
		where := jsonx.NewObject()
		cond := jsonx.NewObject()
		// 路径段按原文比较：字段转成字符串后相等就算匹配，数字和字符串外键都能对上
		// （/articles/1/remarks 配 articleId:"1"）；不查父记录，父缺失时自然得 200 空数组。
		cond.Set("eq", query.LooseBound{Val: jsonx.Value(idParam), Raw: idParam})
		where.Set(fk, cond)
		m, _ := query.CompileWhere(where)
		a.queryMany(w, r, childName, childRes, m)
	case http.MethodPost:
		body := a.readBodyObject(w, r)
		if body == nil {
			return
		}
		if a.opt.Legacy {
			// 0.17 nested.js post：无条件写 body[fk] = params.id（字符串）。
			body.Set(fk, jsonx.Value(idParam))
		} else if _, ok := body.Get(fk); !ok {
			// 父记录存在时用它的 id 原值（数字还是数字，"007" 还是 "007"），
			// 否则用路径原文；不做类型转换，否则 "12e4" 会被写成 120000。
			fkVal := jsonx.Value(idParam)
			if idx := a.findRecord(res.asArray(), idParam); idx >= 0 {
				fkVal = a.recordID(res.asArray()[idx])
			}
			body.Set(fk, fkVal)
		}
		a.createRecordWithBody(w, r, childName, body)
	default:
		writeErr(w, http.StatusNotFound, "unknown route")
	}
}
