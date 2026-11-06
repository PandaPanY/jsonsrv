package httpapi

// POST 空请求体按 {} 处理（两模式 201）、--legacy 新 id 按 0.17 mixins.createId 规则。
import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestEmptyBodyPOST 两个原版都对空请求体按 {} 处理（body-parser 空体策略），返回 201；
// 非法 JSON 与非对象仍 400。默认与 --legacy 行为相同。
func TestEmptyBodyPOST(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "默认模式"
		if legacy {
			name = "--legacy"
		}
		t.Run(name, func(t *testing.T) {
			app, _ := newTestApp(t, `{"posts":[]}`, Options{Legacy: legacy})
			code, _, body := doReq(app, "POST", "/posts", "")
			if code != 201 {
				t.Fatalf("空请求体应 201，得到 %d: %s", code, body)
			}
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(body), &obj); err != nil {
				t.Fatalf("响应应是对象: %v (%s)", err, body)
			}
			if _, ok := obj["id"]; !ok {
				t.Errorf("响应应带生成的 id，得到 %s", body)
			}
			if code, _, _ = doReq(app, "POST", "/posts", "{abc"); code != 400 {
				t.Errorf("非法 JSON 应 400，得到 %d", code)
			}
			if code, _, _ = doReq(app, "POST", "/posts", "[1,2]"); code != 400 {
				t.Errorf("非对象请求体应 400，得到 %d", code)
			}
		})
	}
}

// TestLegacyCreateID 对照 0.17 mixins.createId：空集合 → 1；存在数字 id（含与字符串 id 混合）
// → 数字最大值加 1；纯字符串 id 集合 → 随机字符串（JsonSrv 为 11 位随机串，长度差异已在对照测试登记）。
func TestLegacyCreateID(t *testing.T) {
	cases := []struct {
		name string
		db   string
		want string // 期望 id 原始数字文本；"@randstr" 表示应为随机字符串
	}{
		{"空集合", `{"books":[]}`, "1"},
		{"混有字符串 id 仍取数字最大值加 1", `{"books":[{"id":1},{"id":"abc"},{"id":5}]}`, "6"},
		{"负数 id 取最大值加 1", `{"books":[{"id":-1}]}`, "0"},
		{"纯数字 id", `{"books":[{"id":3},{"id":7}]}`, "8"},
		{"纯字符串 id 生成随机串", `{"books":[{"id":"abc"}]}`, "@randstr"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app, _ := newTestApp(t, c.db, Options{Legacy: true})
			code, _, body := doReq(app, "POST", "/books", "{}")
			if code != 201 {
				t.Fatalf("POST 应 201: %d %s", code, body)
			}
			dec := json.NewDecoder(bytes.NewReader([]byte(body)))
			dec.UseNumber()
			var obj map[string]interface{}
			if err := dec.Decode(&obj); err != nil {
				t.Fatal(err)
			}
			idv, ok := obj["id"]
			if !ok {
				t.Fatalf("响应应带 id: %s", body)
			}
			if c.want == "@randstr" {
				if s, ok := idv.(string); !ok || s == "" {
					t.Errorf("纯字符串 id 集合应生成随机字符串 id，得到 %#v", idv)
				}
				return
			}
			n, ok := idv.(json.Number)
			if !ok {
				t.Fatalf("id 应为数字，得到 %#v", idv)
			}
			if n.String() != c.want {
				t.Errorf("id 应为 %s，得到 %s", c.want, n)
			}
		})
	}
}
