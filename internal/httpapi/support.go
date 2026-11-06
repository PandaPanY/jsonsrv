package httpapi

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// errNotFound 表示目标记录不存在（内部哨兵）。
var errNotFound = errors.New("record not found")

// splitComma 按逗号切分并去除两端空白，忽略空项。
func splitComma(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// maxNumberID 计算 --legacy 下的自增数字 id：
// 现有 id 全为数字时返回最大值加一；存在非数字 id 时返回 ok=false。
// maxNumberID 按 0.17 mixins.createId 的 maxBy 语义返回下一个数字 id：
// 集合中存在整数数字 id（即使与字符串 id 混合）时取最大值加 1；
// 完全没有数字 id（空集合或纯字符串 id）时返回 ok=false，由调用方生成随机串。
func maxNumberID(arr []jsonx.Value, idName string) (json.Number, bool) {
	var max int64
	var has bool
	for _, rec := range arr {
		obj, ok := jsonx.AsObject(rec)
		if !ok {
			continue
		}
		idv, ok := obj.Get(idName)
		if !ok {
			continue
		}
		n, ok := jsonx.AsNumber(idv)
		if !ok {
			continue
		}
		iv, err := n.Int64()
		if err != nil {
			// 小数 id 不参与（0.17 会把它当有限数递增，罕见边界本轮不扩展）
			continue
		}
		if !has || iv > max {
			max = iv
			has = true
		}
	}
	if !has {
		return "", false
	}
	return json.Number(strconv.FormatInt(max+1, 10)), true
}
