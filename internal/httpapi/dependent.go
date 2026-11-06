package httpapi

import (
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/inflect"
	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// dependentDelete 实现 DELETE /资源/:id?_dependent=xxx 的级联删除：
// 在依赖集合中删除外键（单数名+后缀，默认 Id）等于本记录 id 的条目。
func (a *App) dependentDelete(db *jsonx.Object, resID, idParam string, deps []string) {
	fk := inflect.Singular(resID) + a.opt.FKS
	for _, dep := range deps {
		cur, ok := jsonx.AsArray(db.MustGet(dep))
		if !ok {
			continue
		}
		// cur[:0] 复用会就地写共享数组，构造新切片规避。
		kept := make([]jsonx.Value, 0, len(cur))
		for _, rec := range cur {
			if !recordHasValue(rec, fk, idParam) {
				kept = append(kept, rec)
			}
		}
		db.Set(dep, jsonx.Value(kept))
	}
}

// recordHasValue 判断记录的 fk 字段（字符串或数字）等于给定字符串值。
func recordHasValue(rec jsonx.Value, fk, want string) bool {
	obj, ok := jsonx.AsObject(rec)
	if !ok {
		return false
	}
	v, ok := obj.Get(fk)
	if !ok {
		return false
	}
	s, ok := jsonx.ToString(v)
	return ok && s == want
}

// removeOrphans 实现 0.17 mixins.getRemovable：扫描全部集合，删除
// "以 foreignKeySuffix 后缀字段引用了不存在的父记录" 的孤儿记录。
// 引用表名 = 去掉后缀后复数化（postId → posts）；表不存在则跳过。
func (a *App) removeOrphans(db *jsonx.Object) {
	for _, collName := range db.Keys() {
		coll, ok := jsonx.AsArray(db.MustGet(collName))
		if !ok {
			continue
		}
		var orphanIDs []string
		for _, rec := range coll {
			obj, ok := jsonx.AsObject(rec)
			if !ok {
				continue
			}
			orphan := false
			for _, key := range obj.Keys() {
				if !strings.HasSuffix(key, a.opt.FKS) {
					continue
				}
				refName := inflect.Plural(strings.TrimSuffix(key, a.opt.FKS))
				refColl, ok := jsonx.AsArray(db.MustGet(refName))
				if !ok {
					continue
				}
				v, _ := obj.Get(key)
				vs, ok := jsonx.ToString(v)
				if !ok {
					continue
				}
				if a.findRecord(refColl, vs) < 0 {
					orphan = true
					break
				}
			}
			if orphan {
				if idv := a.recordID(rec); idv != nil {
					if s, ok := jsonx.ToString(idv); ok {
						orphanIDs = append(orphanIDs, s)
					}
				}
			}
		}
		if len(orphanIDs) == 0 {
			continue
		}
		kept := make([]jsonx.Value, 0, len(coll))
		for _, rec := range coll {
			drop := false
			if idv := a.recordID(rec); idv != nil {
				if s, ok := jsonx.ToString(idv); ok {
					for _, oid := range orphanIDs {
						if s == oid {
							drop = true
							break
						}
					}
				}
			}
			if !drop {
				kept = append(kept, rec)
			}
		}
		db.Set(collName, kept)
	}
}
