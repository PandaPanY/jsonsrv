package httpapi

// 默认模式 URL 参数值与字符串字段值的比较规则是「按类型相等，
// 或字段值转字符串后与参数原串完全相等」任一成立；数组字段 eq 为元素包含
// （比 0.17 宽松，有意差异）；嵌套路由与 _embed/_expand 外键数字/字符串兼容；
// _where 仍按 JSON 类型比较；大小写敏感不变。数据与 scripts/compare-db3.json 一致。

import (
	"strings"
	"testing"
)

// stringMatchDB 与 scripts/compare-db3.json 内容一致。
const stringMatchDB = `{
  "products": [
    {
      "id": 1,
      "name": "Alpha",
      "price": 10,
      "stock": 2.5,
      "active": true,
      "sku": "007",
      "note": null,
      "dims": {
        "w": 1,
        "h": 5
      },
      "vendorId": 1,
      "tags": [
        "a",
        "b"
      ]
    },
    {
      "id": 2,
      "name": "beta",
      "price": 20,
      "stock": 0,
      "active": false,
      "sku": "7",
      "note": "n",
      "dims": {
        "w": 2,
        "h": 4
      },
      "vendorId": 1,
      "tags": [
        "b"
      ]
    },
    {
      "id": 3,
      "name": "Gamma ray",
      "price": 10,
      "stock": 7,
      "active": true,
      "sku": "x1",
      "dims": {
        "w": 3,
        "h": 3
      },
      "vendorId": 2,
      "tags": []
    },
    {
      "id": 4,
      "name": "delta",
      "price": 40,
      "stock": 1,
      "active": false,
      "sku": "010",
      "note": "",
      "dims": {
        "w": 2,
        "h": 2
      },
      "vendorId": 3,
      "tags": [
        "c"
      ]
    },
    {
      "id": 5,
      "name": "Epsilon",
      "price": 5,
      "stock": 3,
      "active": true,
      "sku": "9",
      "note": "N",
      "vendorId": 2,
      "tags": [
        "a"
      ]
    },
    {
      "id": 6,
      "name": "zeta",
      "price": 25,
      "stock": 9,
      "active": true,
      "sku": "11",
      "note": null,
      "dims": {
        "w": 1,
        "h": 1
      },
      "vendorId": 1,
      "tags": [
        "z"
      ]
    }
  ],
  "vendors": [
    {
      "id": 1,
      "name": "V1"
    },
    {
      "id": 2,
      "name": "V2"
    },
    {
      "id": 3,
      "name": "V3"
    }
  ],
  "news": [
    {
      "id": 1,
      "t": "n"
    }
  ],
  "comments": [
    {
      "id": 1,
      "newsId": 1
    }
  ],
  "people": [
    {
      "id": 1,
      "n": "p"
    }
  ],
  "pets": [
    {
      "id": 1,
      "personId": 1
    }
  ],
  "children": [
    {
      "id": 1,
      "n": "c"
    }
  ],
  "toys": [
    {
      "id": 1,
      "childId": 1
    }
  ],
  "companies": [
    {
      "id": 1
    }
  ],
  "staff": [
    {
      "id": 1,
      "companyId": 1
    }
  ],
  "series": [
    {
      "id": 1
    }
  ],
  "episodes": [
    {
      "id": 1,
      "seriesId": 1
    }
  ],
  "mice": [
    {
      "id": 1
    }
  ],
  "tails": [
    {
      "id": 1,
      "mouseId": 1
    }
  ],
  "analyses": [
    {
      "id": 1
    }
  ],
  "runs": [
    {
      "id": 1,
      "analysisId": 1
    }
  ],
  "aliases": [
    {
      "id": 1
    }
  ],
  "refs": [
    {
      "id": 1,
      "aliasId": 1
    }
  ],
  "buses": [
    {
      "id": 1
    }
  ],
  "seats": [
    {
      "id": 1,
      "busId": 1
    }
  ],
  "heroes": [
    {
      "id": 1
    }
  ],
  "powers": [
    {
      "id": 1,
      "heroId": 1
    }
  ],
  "users": [
    {
      "id": "u 1",
      "n": "sp"
    },
    {
      "id": "u2",
      "n": "x"
    }
  ],
  "posts": [
    {
      "id": 1,
      "userId": "u 1"
    }
  ],
  "settings": {
    "a": 1,
    "b": {
      "c": 2
    }
  },
  "articles": [
    {
      "id": "1",
      "t": "a"
    },
    {
      "id": "2",
      "t": "b"
    }
  ],
  "remarks": [
    {
      "id": 1,
      "articleId": "1"
    },
    {
      "id": 2,
      "articleId": "2"
    },
    {
      "id": 3,
      "articleId": "2"
    }
  ]
}`

// TestStringMatchAcceptance 逐行检查 URL 值和字符串字段的匹配结果。
func TestStringMatchAcceptance(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{})
	tests := []struct {
		path string
		ids  []string
	}{
		{"/products?sku=007", []string{"1"}},
		{"/products?sku=7", []string{"2"}},
		{"/products?sku:eq=007", []string{"1"}},
		{"/products?sku:in=007,9", []string{"1", "5"}},
		{"/products?sku:ne=007", []string{"2", "3", "4", "5", "6"}},
		{"/articles?id=1", []string{"1"}},
		{"/articles?id=1&id=2", []string{"1", "2"}},
		{"/articles?id:in=1,2", []string{"1", "2"}},
		{"/remarks?articleId=1", []string{"1"}},
		{"/remarks?articleId:eq=2", []string{"2", "3"}},
		{"/articles/1/remarks", []string{"1"}},
		{"/products?tags=a", []string{"1", "5"}},
		{"/products?tags=b&tags=c", []string{"1", "2", "4"}},
		{"/products?price=10", []string{"1", "3"}},
		{"/products?name=Alpha", []string{"1"}},
		{"/products?name=alpha", nil},
		{"/products?name:ne=beta", []string{"1", "3", "4", "5", "6"}},
	}
	for _, tc := range tests {
		status, _, body := doReq(app, "GET", tc.path, "")
		if status != 200 {
			t.Fatalf("%s: 状态 %d (%s)", tc.path, status, body)
		}
		if got := idsOf(t, body); !eqIDs(got, tc.ids) {
			t.Errorf("%s: id %v, 期望 %v", tc.path, got, tc.ids)
		}
	}
}

// TestStringMatchSliceAfterNested：/articles/2/remarks?_limit=1 → [2] 且 X-Total-Count: 2。
func TestStringMatchSliceAfterNested(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{})
	status, head, body := doReq(app, "GET", "/articles/2/remarks?_limit=1", "")
	if status != 200 {
		t.Fatalf("状态 %d (%s)", status, body)
	}
	if !eqIDs(idsOf(t, body), []string{"2"}) {
		t.Errorf("id 不符: %v (%s)", idsOf(t, body), body)
	}
	if got := head.Get("X-Total-Count"); got != "2" {
		t.Errorf("X-Total-Count = %q, 期望 2", got)
	}
}

// TestStringMatchEmbedExpandStringID：_embed=remarks 命中的子记录带外键；
// _expand=article 命中的 article.id 为字符串 "1"（响应原样输出）。
func TestStringMatchEmbedExpandStringID(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{})
	_, _, body := doReq(app, "GET", "/articles/1?_embed=remarks", "")
	if !strings.Contains(body, "\"articleId\":\"1\"") {
		t.Errorf("_embed=remarks 应内嵌 articleId 为 1 的记录: %s", body)
	}
	_, _, body = doReq(app, "GET", "/remarks/1?_expand=article", "")
	if !strings.Contains(body, "\"article\":{") || !strings.Contains(body, "\"id\":\"1\"") {
		t.Errorf("_expand=article 应为字符串 id 的 article: %s", body)
	}
}

// TestStringMatchLegacyArrayField：--legacy 保持 0.17 的数组字段行为（转字符串比较）：
// tags=a 得 [5]（["a"].toString()=="a"），tags=b&tags=c 得 [2,4]。
// 默认模式的包含语义已在 TestStringMatchAcceptance 断言（[1,5] 与 [1,2,4]）。
func TestStringMatchLegacyArrayField(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{Legacy: true})
	_, _, body := doReq(app, "GET", "/products?tags=a", "")
	if !eqIDs(idsOf(t, body), []string{"5"}) {
		t.Errorf("--legacy tags=a 期望 [5]: %v (%s)", idsOf(t, body), body)
	}
	_, _, body = doReq(app, "GET", "/products?tags=b&tags=c", "")
	if !eqIDs(idsOf(t, body), []string{"2", "4"}) {
		t.Errorf("--legacy tags=b&tags=c 期望 [2 4]: %v (%s)", idsOf(t, body), body)
	}
}

// TestStringMatchLegacyNested：GET /articles/1/remarks 在 --legacy 同样返回 [1]。
func TestStringMatchLegacyNested(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{Legacy: true})
	status, _, body := doReq(app, "GET", "/articles/1/remarks", "")
	if status != 200 || !eqIDs(idsOf(t, body), []string{"1"}) {
		t.Errorf("--legacy /articles/1/remarks 期望 200 [1]: %d %v (%s)", status, idsOf(t, body), body)
	}
}

// TestStringMatchWhereStillTyped：_where 有明确的 JSON 类型，仍按类型比较（不做字符串回退）。
func TestStringMatchWhereStillTyped(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{})
	_, _, body := doReq(app, "GET", "/products?_where=%7B%22sku%22%3A%7B%22eq%22%3A%22007%22%7D%7D", "")
	if !eqIDs(idsOf(t, body), []string{"1"}) {
		t.Errorf("_where sku eq \"007\" 期望 [1]: %v (%s)", idsOf(t, body), body)
	}
	// JSON 数字 7 与字符串 "7" 按类型不等：v1 语义保持查不到。
	_, _, body = doReq(app, "GET", "/products?_where=%7B%22sku%22%3A%7B%22eq%22%3A7%7D%7D", "")
	if got := idsOf(t, body); len(got) != 0 {
		t.Errorf("_where sku eq 7（数字）期望空: %v (%s)", got, body)
	}
}

// TestStringMatchNestedFKLoose：嵌套路由与 _expand 的外键在数字与字符串之间都要能对上。
func TestStringMatchNestedFKLoose(t *testing.T) {
	db := `{"articles":[{"id":1,"t":"x"}],"remarks":[{"id":1,"articleId":"1"},{"id":2,"articleId":"2"}]}`
	app, _ := newTestApp(t, db, Options{})
	status, _, body := doReq(app, "GET", "/articles/1/remarks", "")
	if status != 200 || !eqIDs(idsOf(t, body), []string{"1"}) {
		t.Errorf("数字父 id 配字符串外键期望 200 [1]: %d %v (%s)", status, idsOf(t, body), body)
	}
	_, _, body = doReq(app, "GET", "/remarks/1?_expand=article", "")
	if !strings.Contains(body, "\"article\":{") {
		t.Errorf("字符串外键 1 应能展开数字 id 的 article: %s", body)
	}
}
