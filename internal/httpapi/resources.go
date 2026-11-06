package httpapi

import (
	"encoding/json"
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// resourceInfo 是一个按请求解析出来的资源。
type resourceInfo struct {
	singular bool // 值是对象；false 则值是数组
	val      jsonx.Value
}

// splitPath 把 URL 路径拆为段（去空段）。
func splitPath(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	seg := parts[:0]
	for _, s := range parts {
		if s != "" {
			seg = append(seg, s)
		}
	}
	return seg
}

// splitFirst 取第一段及剩余段。
func splitFirst(seg []string) (string, []string) {
	if len(seg) == 0 {
		return "", nil
	}
	return seg[0], seg[1:]
}

// resource 按名称查找单个资源；未定义或非集合/单例返回 ok=false。
func (a *App) resource(snap *jsonx.Object, name string) (resourceInfo, bool) {
	val, ok := snap.Get(name)
	if !ok {
		return resourceInfo{}, false
	}
	switch val.(type) {
	case *jsonx.Object:
		return resourceInfo{singular: true, val: val}, true
	case []jsonx.Value:
		return resourceInfo{val: val}, true
	}
	return resourceInfo{}, false
}

// asArray 把资源值转为记录数组（快照共享、不可变）。
func (ri resourceInfo) asArray() []jsonx.Value {
	arr, _ := jsonx.AsArray(ri.val)
	if arr == nil {
		return []jsonx.Value{}
	}
	return arr
}

// findRecord 按 id 字符串查找记录索引；找不到返回 -1。
// 数字 id 比较数值，字符串 id 精确匹配。
func (a *App) findRecord(arr []jsonx.Value, idParam string) int {
	for i, rec := range arr {
		if recordMatches(rec, a.opt.IDName, idParam) {
			return i
		}
	}
	return -1
}

func recordMatches(rec jsonx.Value, idName, idParam string) bool {
	obj, ok := jsonx.AsObject(rec)
	if !ok {
		return false
	}
	idVal, ok := obj.Get(idName)
	if !ok {
		return false
	}
	switch id := idVal.(type) {
	case string:
		return id == idParam
	case json.Number:
		return id.String() == idParam
	}
	return false
}
