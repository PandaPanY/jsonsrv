package httpapi

// 默认模式嵌套路由与 0.17 一致——父记录不存在时 GET 返回 200 空数组
// （/vendors/abc/products ≡ /products?vendorId=abc），不是 404；
// 子资源不存在仍是 404；路径外键优先于同名查询参数（/vendors/1/products?vendorId=2 → [1,2,6]）。

import (
	"strings"
	"testing"
)

func TestDefaultNestedParentMissing(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{})

	// 父不存在 → 200 空数组
	status, _, body := doReq(app, "GET", "/vendors/abc/products", "")
	if status != 200 || !strings.Contains(body, "[]") {
		t.Fatalf("GET /vendors/abc/products：状态 %d 体 %s，期望 200 []", status, body)
	}
	if ids := idsOf(t, body); len(ids) != 0 {
		t.Errorf("GET /vendors/abc/products：id %v，期望空", ids)
	}

	// 子资源不存在 → 404
	if status, _, _ := doReq(app, "GET", "/vendors/1/nothing", ""); status != 404 {
		t.Errorf("GET /vendors/1/nothing：状态 %d，期望 404", status)
	}

	// 路径外键优先于查询参数（0.17 nested.js：query[fk] = params.id 覆盖）
	status, _, body = doReq(app, "GET", "/vendors/1/products?vendorId=2", "")
	if status != 200 {
		t.Fatalf("GET /vendors/1/products?vendorId=2：状态 %d (%s)", status, body)
	}
	got := idsOf(t, body)
	want := []string{"1", "2", "6"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("GET /vendors/1/products?vendorId=2：id %v，期望 %v", got, want)
	}
}

func TestDefaultNestedPostParentMissing(t *testing.T) {
	app, _ := newTestApp(t, stringMatchDB, Options{})
	// 0.17 nested POST 不检查父存在；默认模式等价于补外键的 POST（201，id 自动生成）。
	status, _, body := doReq(app, "POST", "/vendors/999/products", "{\"name\":\"x\"}")
	if status != 201 {
		t.Fatalf("POST /vendors/999/products：状态 %d (%s)，期望 201", status, body)
	}
	if !strings.Contains(body, "\"vendorId\":999") && !strings.Contains(body, "\"vendorId\":\"999\"") {
		t.Errorf("POST /vendors/999/products：响应未带外键： %s", body)
	}
}
