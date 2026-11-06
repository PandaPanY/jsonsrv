package httpapi

// 共享测试断言工具（不带构建标签，race 模式下也可用）。

import (
	"encoding/json"
	"strings"
	"testing"
)

// firstN 返回 s 的前 n 个字符（rune 安全）。
func firstN(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// dataOf 若 body 是 v1 分页包裹对象则返回其中 data 字段的原文。
func dataOf(t *testing.T, body string) string {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &env); err == nil {
		if d, ok := env["data"]; ok {
			return string(d)
		}
	}
	return body
}

// splitCSV 拆分逗号分隔串并去空白（空串返回 nil）。
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
