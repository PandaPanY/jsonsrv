package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// idAlphabet 与 json-server v1（nanoid 字母表）同族随机串。
const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

const maxBodyBytes = 32 << 20

// newStringID 生成 11 字符随机 id（v1 实测为 11 位字符串短随机串）。
func newStringID() string {
	buf := make([]byte, 11)
	max := big.NewInt(int64(len(idAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err) // crypto/rand 失败视为致命
		}
		buf[i] = idAlphabet[n.Int64()]
	}
	return string(buf)
}

// handleCollection 处理 /资源 与 /资源/:id（segID 为空表示前者）。
func (a *App) handleCollection(w http.ResponseWriter, r *http.Request, resID string, res resourceInfo, segID string) {
	switch r.Method {
	case http.MethodGet:
		if segID == "" {
			a.queryMany(w, r, resID, res)
			return
		}
		a.queryOne(w, r, resID, res, segID)
	case http.MethodPost:
		if segID != "" {
			writeErr(w, http.StatusNotFound, "unknown route")
			return
		}
		a.createRecord(w, r, resID, res)
	default:
		if segID != "" {
			a.mutateRecord(w, r, resID, res, segID)
			return
		}
		writeErr(w, http.StatusNotFound, "unknown route")
	}
}

// createRecord 实现 POST：自动生成 id、拒绝重复 id、返回 201。
func (a *App) createRecord(w http.ResponseWriter, r *http.Request, resID string, res resourceInfo) {
	body := a.readBodyObject(w, r)
	if body == nil {
		return
	}
	a.createRecordWithBody(w, r, resID, body)
}

// errIDDup 表示显式 id 与现有记录冲突（POST 在写锁回调内检测）。
var errIDDup = errors.New("id already exists")

// errIDNotScalar 表示请求体 id 为对象/数组（返回 400）。
var errIDNotScalar = errors.New("id must be a scalar value")

// isFalsyID 判断 id 是否为 falsy（null/false/0/""），视为未传。
// JSON 数字按 JSON 真值语义判定（0 为 falsy；NaN/Inf 恒为 truthy，不影响结果）。
func isFalsyID(v jsonx.Value) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case bool:
		return !t
	case string:
		return t == ""
	case json.Number:
		f, err := t.Float64()
		return err == nil && f == 0
	default:
		return false
	}
}

// createRecordWithBody 是 POST 的公共逻辑（嵌套路由会先注入外键再调用）。
// id 查重与生成都放在写锁回调内，用回调拿到的最新数据，
// 避免并发请求基于写锁外的快照产生重复 id。
func (a *App) createRecordWithBody(w http.ResponseWriter, r *http.Request, resID string, body *jsonx.Object) {
	var newRec *jsonx.Object
	var dupID string
	err := a.st.Update(func(db *jsonx.Object) error {
		cur, _ := jsonx.AsArray(db.MustGet(resID))
		idv, hasID := body.Get(a.opt.IDName)
		if hasID && !isFalsyID(idv) {
			// 显式非 falsy id —— 对象/数组视为 400；字符串查重语义不做类型归一化
			if !jsonx.IsScalarLike(idv) {
				return errIDNotScalar
			}
			if s, ok := jsonx.ToString(idv); ok && a.findRecord(cur, s) >= 0 {
				dupID = s
				return errIDDup
			}
			newRec = body
		} else {
			if hasID {
				// falsy id（null/false/0/""）视为未传，删键走自动生成（与 0.17 mixins.createId 一致）
				body.Delete(a.opt.IDName)
			}
			newRec = body.Clone()
			a.assignID(newRec, cur)
		}
		// append 可能写入共享数组的空余容量，构造新切片规避。
		next := make([]jsonx.Value, 0, len(cur)+1)
		next = append(next, cur...)
		next = append(next, newRec)
		db.Set(resID, jsonx.Value(next))
		return nil
	})
	switch {
	case errors.Is(err, errIDNotScalar):
		writeErr(w, http.StatusBadRequest, "id must be a scalar value")
		return
	case errors.Is(err, errIDDup):
		writeErr(w, http.StatusConflict, "id \""+dupID+"\" already exists")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if a.opt.Legacy {
		// 0.17 plural.js：POST 201 附带 Expose: Location 与 Location（完整资源 URL）。
		idStr := ""
		if v := a.recordID(newRec); v != nil {
			idStr, _ = jsonx.ToString(v)
		}
		w.Header().Set("Access-Control-Expose-Headers", "Location")
		if idStr != "" {
			w.Header().Set("Location", requestURL(r)+"/"+idStr)
		}
	}
	writeJSON(w, http.StatusCreated, newRec)
}

// mutateRecord 处理 PUT/PATCH/DELETE /资源/:id。
func (a *App) mutateRecord(w http.ResponseWriter, r *http.Request, resID string, res resourceInfo, idParam string) {
	switch r.Method {
	case http.MethodPut:
		body := a.readBodyObject(w, r)
		if body == nil {
			return
		}
		// 存在性判断与替换都在写锁回调内做，避免"查到后被并发删除"
		// 导致 200 但没有任何修改。id 不可修改：请求体里的 id 被忽略。
		var replacement *jsonx.Object
		err := a.st.Update(func(db *jsonx.Object) error {
			cur, _ := jsonx.AsArray(db.MustGet(resID))
			i := a.findRecord(cur, idParam)
			if i < 0 {
				return errNotFound
			}
			oldID := a.recordID(cur[i])
			replacement = jsonx.NewObject()
			if oldID != nil {
				replacement.Set(a.opt.IDName, oldID)
			}
			for _, k := range body.Keys() {
				if k != a.opt.IDName {
					replacement.Set(k, body.MustGet(k))
				}
			}
			// store.Update 只做顶层浅拷贝，这里必须构造新切片，
			// 不得就地写共享数组（旧快照的读方可能正在遍历）。
			next := make([]jsonx.Value, len(cur))
			copy(next, cur)
			next[i] = replacement
			db.Set(resID, jsonx.Value(next))
			return nil
		})
		if errors.Is(err, errNotFound) {
			writeErr(w, http.StatusNotFound, "record not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, replacement)

	case http.MethodPatch:
		body := a.readBodyObject(w, r)
		if body == nil {
			return
		}
		var merged *jsonx.Object
		err := a.st.Update(func(db *jsonx.Object) error {
			cur, _ := jsonx.AsArray(db.MustGet(resID))
			i := a.findRecord(cur, idParam)
			if i < 0 {
				return errNotFound
			}
			m, ok := jsonx.AsObject(cur[i])
			if !ok {
				return errNotFound
			}
			// 克隆记录后再合并，共享旧快照里的原记录不被就地修改。
			merged = m.Clone()
			for _, k := range body.Keys() {
				if k != a.opt.IDName { // id 不可修改
					merged.Set(k, body.MustGet(k))
				}
			}
			next := make([]jsonx.Value, len(cur))
			copy(next, cur)
			next[i] = merged
			db.Set(resID, jsonx.Value(next))
			return nil
		})
		if err == errNotFound {
			writeErr(w, http.StatusNotFound, "record not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, merged)

	case http.MethodDelete:
		// 同名 `_dependent` 重复出现时全部生效（逗号分隔仍支持）。
		dependent := queryValuesAll(r.URL.Query(), "_dependent")
		// 存在性判断与被删记录都在写锁回调内取，避免并发下的
		// "200 但什么都没删"。响应体：legacy 对照 0.17.4 实测为空对象。
		var removed jsonx.Value
		err := a.st.Update(func(db *jsonx.Object) error {
			cur, _ := jsonx.AsArray(db.MustGet(resID))
			i := a.findRecord(cur, idParam)
			if i < 0 {
				return errNotFound
			}
			removed = cur[i]
			// cur[:i]+cur[i+1:] 会就地搬移共享数组，构造新切片规避。
			next := make([]jsonx.Value, 0, len(cur)-1)
			next = append(next, cur[:i]...)
			next = append(next, cur[i+1:]...)
			db.Set(resID, jsonx.Value(next))
			a.dependentDelete(db, resID, idParam, dependent)
			// 0.17 destroy + mixins.getRemovable：默认级联是全局
			// 清理"外键后缀字段指向不存在的父记录"的孤儿记录，
			// 与是否传 _dependent 无关；默认（v1）模式不做。
			if a.opt.Legacy {
				a.removeOrphans(db)
			}
			return nil
		})
		if errors.Is(err, errNotFound) {
			writeErr(w, http.StatusNotFound, "record not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if a.opt.Legacy {
			writeJSON(w, http.StatusOK, jsonx.NewObject())
			return
		}
		writeJSON(w, http.StatusOK, removed)

	default:
		writeErr(w, http.StatusNotFound, "unknown route")
	}
}

// handleSingular 处理单例资源的 GET/PUT/PATCH（以及 --legacy 下的 POST，
// 0.17 singular.js：POST 即 set，返回 201 + Location）。
func (a *App) handleSingular(w http.ResponseWriter, r *http.Request, resID string, res resourceInfo) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, res.val)
	case http.MethodPost:
		if !a.opt.Legacy {
			writeErr(w, http.StatusNotFound, "unknown route")
			return
		}
		body := a.readBodyObject(w, r)
		if body == nil {
			return
		}
		if err := a.st.Update(func(db *jsonx.Object) error {
			db.Set(resID, body)
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 0.17 实测：单例 POST 201，Location 为资源完整 URL，Expose: Location。
		w.Header().Set("Access-Control-Expose-Headers", "Location")
		w.Header().Set("Location", requestURL(r))
		writeJSON(w, http.StatusCreated, body)
	case http.MethodPut:
		body := a.readBodyObject(w, r)
		if body == nil {
			return
		}
		if err := a.st.Update(func(db *jsonx.Object) error {
			db.Set(resID, body)
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, body)
	case http.MethodPatch:
		body := a.readBodyObject(w, r)
		if body == nil {
			return
		}
		var merged *jsonx.Object
		if err := a.st.Update(func(db *jsonx.Object) error {
			cur, _ := jsonx.AsObject(db.MustGet(resID))
			// 克隆后再合并，旧快照里的原对象不被就地修改。
			merged = cur.Clone()
			for _, k := range body.Keys() {
				merged.Set(k, body.MustGet(k))
			}
			db.Set(resID, jsonx.Value(merged))
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, merged)
	default:
		writeErr(w, http.StatusNotFound, "unknown route")
	}
}

// readBodyObject 解析请求体为 JSON 对象；非法 JSON 或非对象时写 400 并返回 nil。
// --legacy（0.17 实测，body-parser 只挂 json 与 urlencoded）：
//   - Content-Type 为表单 → 按 urlencoded 解析（值为字符串）；
//   - 其他非 JSON 类型（如 text/plain）→ 不解析请求体，按空对象创建；
//
// 默认（v1）模式保持原样：任意 Content-Type 都按 JSON 解析（milliparsec 行为，
// text/plain 带 JSON 也解析）。
func (a *App) readBodyObject(w http.ResponseWriter, r *http.Request) *jsonx.Object {
	var data []byte
	var err error
	if r.Body != nil {
		data, err = io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read the request body")
		return nil
	}
	// 两个原版都对空请求体按 {} 处理（body-parser 的空体策略），返回 201；
	// 非法 JSON 与非对象仍是 400（见下方解析分支）。
	if len(data) == 0 {
		return jsonx.NewObject()
	}
	if a.opt.Legacy {
		switch baseCT(r) {
		case "application/x-www-form-urlencoded":
			return formToObject(string(data))
		case "application/json", "":
			// JSON 照常解析；无 Content-Type 也按 JSON（与 0.17 一致：
			// body-parser 默认只认 application/json，缺失时按 JSON 解析请求体）。
		default:
			return jsonx.NewObject() // 其他类型：请求体被忽略
		}
	}
	v, err := jsonx.Parse(data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "request body is not valid JSON")
		return nil
	}
	obj, ok := jsonx.AsObject(v)
	if !ok {
		writeErr(w, http.StatusBadRequest, "request body must be a JSON object")
		return nil
	}
	return obj
}

// baseCT 取请求 Content-Type 分号前缀的媒体类型（小写去空格）。
func baseCT(r *http.Request) string {
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// formToObject 按 urlencoded（extended:false，拍平一层）解析表单为对象，
// 键值均为字符串、顺序保留；同名多键收成字符串数组（querystring 的 [] 语义）。
func formToObject(data string) *jsonx.Object {
	obj := jsonx.NewObject()
	for data != "" {
		pair := data
		if i := strings.IndexByte(data, '&'); i >= 0 {
			pair, data = data[:i], data[i+1:]
		} else {
			data = ""
		}
		if pair == "" {
			continue
		}
		k, v := pair, ""
		if i := strings.IndexByte(pair, '='); i >= 0 {
			k, v = pair[:i], pair[i+1:]
		}
		if ku, err := url.QueryUnescape(k); err == nil {
			k = ku
		}
		if vu, err := url.QueryUnescape(v); err == nil {
			v = vu
		}
		if old, ok := obj.Get(k); ok {
			if arr, ok2 := jsonx.AsArray(old); ok2 {
				obj.Set(k, append(arr, jsonx.Value(v)))
			} else {
				oldStr, _ := jsonx.ToString(old)
				obj.Set(k, []jsonx.Value{jsonx.Value(oldStr), jsonx.Value(v)})
			}
			continue
		}
		obj.Set(k, jsonx.Value(v))
	}
	return obj
}

// recordID 取记录的 id 字段值。
func (a *App) recordID(rec jsonx.Value) jsonx.Value {
	obj, ok := jsonx.AsObject(rec)
	if !ok {
		return nil
	}
	v, _ := obj.Get(a.opt.IDName)
	return v
}

// assignID 为新记录分配 id：默认 v1 随机串；--legacy 按 0.17 mixins.createId：
// 空集合从 1 开始，存在数字 id（含与字符串 id 混合）时取最大值加 1，
// 纯字符串 id 集合生成随机串（JsonSrv 为 11 位，与 0.17 的 7 位仅长度差异，已在对照测试登记）。
func (a *App) assignID(rec *jsonx.Object, arr []jsonx.Value) {
	if a.opt.Legacy {
		if len(arr) == 0 {
			rec.Set(a.opt.IDName, jsonx.Value(json.Number("1")))
			return
		}
		if next, ok := maxNumberID(arr, a.opt.IDName); ok {
			rec.Set(a.opt.IDName, jsonx.Value(next))
			return
		}
	}
	rec.Set(a.opt.IDName, jsonx.Value(newStringID()))
}
