package httpapi

// 默认（v1）模式必须识别 v0.17 写法——_start/_end/_limit 切片、
// _sort+_order 逐字段方向、field_like 正则、同名参数重复、_expand、_page 配 _limit
// 页大小、_where {"and":[...]}。数据与 scripts/compare-db2.json 一致。

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// v017SyntaxDB 与 scripts/compare-db2.json 内容一致。
const v017SyntaxDB = `{
  "books": [
    {
      "id": 1,
      "title": "Go Lang",
      "price": 10,
      "rating": 4.5,
      "inStock": true,
      "categoryId": 1,
      "authorId": "a1",
      "labels": [
        "x",
        "y"
      ],
      "meta": {
        "lang": "en",
        "pages": 300
      }
    },
    {
      "id": 2,
      "title": "go tools",
      "price": 20,
      "rating": 3.0,
      "inStock": false,
      "categoryId": 2,
      "authorId": "a2",
      "labels": [
        "y"
      ],
      "meta": {
        "lang": "zh",
        "pages": 150
      }
    },
    {
      "id": 3,
      "title": "Rust <&> Book",
      "price": 5,
      "rating": 4.5,
      "inStock": true,
      "categoryId": 1,
      "authorId": "a1",
      "labels": [],
      "meta": {
        "lang": "en",
        "pages": 90
      }
    },
    {
      "id": 4,
      "title": "中文书",
      "price": 30,
      "rating": null,
      "inStock": true,
      "categoryId": 99,
      "authorId": "a3",
      "labels": [
        "z"
      ],
      "meta": {
        "lang": "zh",
        "pages": 500
      }
    },
    {
      "id": 5,
      "title": "Zig",
      "price": 15,
      "inStock": false,
      "categoryId": 2,
      "authorId": "a2",
      "labels": [
        "x"
      ],
      "meta": {
        "lang": "en"
      }
    }
  ],
  "categories": [
    {
      "id": 1,
      "name": "lang"
    },
    {
      "id": 2,
      "name": "tool"
    }
  ],
  "authors": [
    {
      "id": "a1",
      "name": "Ann"
    },
    {
      "id": "a2",
      "name": "Bob"
    },
    {
      "id": "a3",
      "name": "Cy"
    }
  ],
  "boxes": [
    {
      "id": 1,
      "w": 1
    }
  ],
  "items": [
    {
      "id": 1,
      "boxId": 1
    }
  ],
  "statuses": [
    {
      "id": 1,
      "s": "on"
    }
  ],
  "logs": [
    {
      "id": 1,
      "statusId": 1
    }
  ],
  "empties": [],
  "nums": [
    {
      "id": 1,
      "big": 12345678901234567890,
      "f": 1.1,
      "e": 1000.0
    }
  ],
  "settings": {
    "theme": "dark",
    "n": 1
  }
}`

// TestDefaultV017Acceptance 逐行检查响应内容与 X-Total-Count。
func TestDefaultV017Acceptance(t *testing.T) {
	app, _ := newTestApp(t, v017SyntaxDB, Options{})
	tests := []struct {
		path string
		ids  []string
		xtc  string // X-Total-Count 期望值（空串表示不应出现）
	}{
		{"/books?_limit=2", []string{"1", "2"}, "5"},
		{"/books?_start=1&_end=3", []string{"2", "3"}, "5"},
		{"/books?_start=1&_limit=2", []string{"2", "3"}, "5"},
		{"/books?_limit=0", nil, "5"},
		{"/books?_limit=abc", nil, "5"},
		// 仅 _start：同 0.17 不切片、不发 X-Total-Count（_end||_limit||_page 皆假）。
		{"/books?_start=5", []string{"1", "2", "3", "4", "5"}, ""},
		{"/books?_sort=price&_order=desc", []string{"4", "2", "5", "1", "3"}, ""},
		{"/books?_sort=price&_order=asc", []string{"3", "1", "5", "2", "4"}, ""},
		// "-" 前缀以前缀为准，忽略同位 _order。
		{"/books?_sort=-price&_order=asc", []string{"4", "2", "5", "1", "3"}, ""},
		// 多字段逐位置对应方向：categoryId 升、price 降。
		{"/books?_sort=categoryId,price&_order=asc,desc", []string{"1", "3", "2", "5", "4"}, ""},
		{"/books?title_like=go", []string{"1", "2"}, ""},
		{"/books?title_like=GO", []string{"1", "2"}, ""}, // 不区分大小写
		{"/books?title_like=^go", []string{"1", "2"}, ""},
		{"/books?id=1&id=2", []string{"1", "2"}, ""},
		{"/books?categoryId=1&categoryId=2&inStock=true", []string{"1", "3"}, ""},
		{"/books?price_ne=10&price_ne=20", []string{"3", "4", "5"}, ""},
		{"/books?price_gte=10", []string{"1", "2", "4", "5"}, ""}, // id5 价格 15 ≥ 10
		{"/books?price_lte=15", []string{"1", "3", "5"}, ""},
		{"/books?q=rust", []string{"3"}, ""},
	}
	for _, tc := range tests {
		status, head, body := doReq(app, "GET", tc.path, "")
		if status != 200 {
			t.Fatalf("%s: 状态 %d (%s)", tc.path, status, body)
		}
		if got := idsOf(t, body); !eqIDs(got, tc.ids) {
			t.Errorf("%s: id %v, 期望 %v", tc.path, got, tc.ids)
		}
		if got := head.Get("X-Total-Count"); got != tc.xtc {
			t.Errorf("%s: X-Total-Count = %q, 期望 %q", tc.path, got, tc.xtc)
		}
		if tc.xtc != "" {
			// 0.17 切片语义：有切片时暴露 X-Total-Count 供前端读取。
			if got := head.Get("Access-Control-Expose-Headers"); got != "X-Total-Count" {
				t.Errorf("%s: Access-Control-Expose-Headers = %q, 期望 X-Total-Count", tc.path, got)
			}
		}
	}
}

// TestDefaultV017Expand 验证默认模式 _expand（0.17 语义：目标集合复数化+外键取父记录；
// 同名重复全部生效；单条与列表都支持；未命中不写该键）。
func TestDefaultV017Expand(t *testing.T) {
	app, _ := newTestApp(t, v017SyntaxDB, Options{})
	_, _, body := doReq(app, "GET", "/books?_expand=category", "")
	var books []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &books); err != nil {
		t.Fatalf("%s", body)
	}
	if len(books) != 5 {
		t.Fatalf("_expand 不应影响结果集: %d 条", len(books))
	}
	for _, b := range books {
		_, has := b["category"]
		// id 4 的 categoryId=99 未命中：不写 category 键。
		want := string(b["id"]) != "4"
		if has != want {
			t.Errorf("book %s category 存在=%v 期望=%v", b["id"], has, want)
		}
	}
	if !strings.Contains(string(books[0]["category"]), "\"name\":\"lang\"") {
		t.Errorf("book1.category 不符: %s", books[0]["category"])
	}
	// 单条记录也支持 _expand。
	_, _, body = doReq(app, "GET", "/books/1?_expand=category", "")
	if !strings.Contains(body, "\"category\"") {
		t.Errorf("单条 _expand 不符: %s", body)
	}
	// 同名重复全部生效。
	_, _, body = doReq(app, "GET", "/books/1?_expand=category&_expand=author", "")
	if !strings.Contains(body, "\"category\"") || !strings.Contains(body, "\"author\"") {
		t.Errorf("重复 _expand 不符: %s", body)
	}
}

// TestDefaultV017PageLimit 验证 _page 配 _limit（无 _per_page 时 _limit 当每页条数，
// 仍是 v1 包裹对象）；_per_page 存在时以其为准。
func TestDefaultV017PageLimit(t *testing.T) {
	app, _ := newTestApp(t, v017SyntaxDB, Options{})
	_, _, body := doReq(app, "GET", "/books?_page=1&_limit=2", "")
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("应为包裹对象: %s", body)
	}
	if string(env["items"]) != "5" || string(env["pages"]) != "3" {
		t.Errorf("items/pages 不符: %s", body)
	}
	if !eqIDs(idsOf(t, body), []string{"1", "2"}) {
		t.Errorf("_page=1&_limit=2 data 不符: %s", body)
	}
	_, _, body = doReq(app, "GET", "/books?_page=2&_limit=2", "")
	if !eqIDs(idsOf(t, body), []string{"3", "4"}) {
		t.Errorf("_page=2&_limit=2 data 不符: %s", body)
	}
	// _per_page 优先于 _limit。
	_, _, body = doReq(app, "GET", "/books?_page=1&_per_page=3&_limit=2", "")
	if !eqIDs(idsOf(t, body), []string{"1", "2", "3"}) {
		t.Errorf("_per_page 应优先: %s", body)
	}
}

// TestDefaultV017WhereAnd 验证 _where {"and":[...]} 全部满足。
func TestDefaultV017WhereAnd(t *testing.T) {
	app, _ := newTestApp(t, v017SyntaxDB, Options{})
	where := "{\"and\":[{\"price\":{\"gt\":5}},{\"inStock\":{\"eq\":true}}]}"
	_, _, body := doReq(app, "GET", "/books?_where="+url.QueryEscape(where), "")
	if got := idsOf(t, body); !eqIDs(got, []string{"1", "4"}) {
		t.Errorf("_where and 结果 %v, 期望 [1 4]: %s", got, body)
	}
}
