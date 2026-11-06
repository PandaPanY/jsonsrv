package httpapi

import (
	"encoding/json"
	"testing"
)

// TestFalsyID 验证 id 为 null/false/0/"" 时视为未传并自动生成，
// id 为对象或数组时返回 400；显式非 falsy id 行为不变（保留并查重）。默认模式与 --legacy 行为一致。
func TestFalsyID(t *testing.T) {
	db := `{"items": [{"id": 1, "n": "x"}]}`
	for _, mode := range []struct {
		name   string
		legacy bool
	}{{"default", false}, {"legacy", true}} {
		t.Run(mode.name, func(t *testing.T) {
			app, _ := newTestApp(t, db, Options{Legacy: mode.legacy})
			// falsy 四种：期望 201 且响应 id 自动生成（非该 falsy 值）。
			for _, m := range []struct{ raw, mark string }{
				{"null", "nnull"}, {"false", "nfalse"}, {"0", "n0"}, {`""`, "ndq"},
			} {
				st, _, body := doReq(app, "POST", "/items", `{"id": `+m.raw+`, "n": "`+m.mark+`"}`)
				if st != 201 {
					t.Fatalf("POST id=%s: 期望 201，得 %d: %s", m.raw, st, body)
				}
				var obj map[string]json.RawMessage
				if err := json.Unmarshal([]byte(body), &obj); err != nil {
					t.Fatalf("POST id=%s: 响应非法: %v (%s)", m.raw, err, body)
				}
				rawID := string(obj["id"])
				if rawID == "" || rawID == "null" || rawID == "false" || rawID == "0" || rawID == `""` {
					t.Fatalf("POST id=%s: 期望自动生成非 falsy id，得 %s: %s", m.raw, rawID, body)
				}
			}
			// 对象/数组 id：400（POST）。
			for _, v := range []string{`{"a":1}`, `[1]`} {
				st, _, body := doReq(app, "POST", "/items", `{"id": `+v+`"}`)
				if st != 400 {
					t.Fatalf("POST id=%s: 期望 400，得 %d: %s", v, st, body)
				}
			}
			// 显式非 falsy 不变：字符串 id 201、重复 409，数字 id 201。
			if st, _, _ := doReq(app, "POST", "/items", `{"id": "abc", "n": 1}`); st != 201 {
				t.Fatalf("显式字符串 id: 期望 201，得 %d", st)
			}
			if st, _, _ := doReq(app, "POST", "/items", `{"id": "abc"}`); st != 409 {
				t.Fatalf("显式字符串 id 查重: 期望 409，得 %d", st)
			}
			if st, _, _ := doReq(app, "POST", "/items", `{"id": 7, "n": 2}`); st != 201 {
				t.Fatalf("显式数字 id: 期望 201，得 %d", st)
			}
			// 回读：falsy POST 生成的记录必须可按标记查询到，且 id 非 falsy。
			_, _, body := doReq(app, "GET", "/items?n=nfalse", "")
			var got []map[string]json.RawMessage
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("回读非法: %v (%s)", err, body)
			}
			if len(got) != 1 {
				t.Fatalf("回读应恰有 1 条，得 %d: %s", len(got), body)
			}
			rawID := string(got[0]["id"])
			if rawID == "" || rawID == "null" || rawID == "false" || rawID == "0" {
				t.Fatalf("回读记录 id 应为自动生成: %s", body)
			}
		})
	}
}
