package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/PandaPanY/jsonsrv/internal/store"
)

// sampleDB 是覆盖集合、嵌套对象与单例的示例库。
const sampleDB = `{
  "posts": [
    { "id": 1, "title": "first post", "author": { "name": "alice" } },
    { "id": 2, "title": "second post", "author": { "name": "bob" }, "views": 10 }
  ],
  "comments": [
    { "id": 1, "body": "nice", "postId": 1 },
    { "id": 2, "body": "good", "postId": 2 }
  ],
  "profile": { "name": "typicode" }
}`

// newTestApp 创建绑定临时数据文件的 App。
func newTestApp(t testing.TB, data string, opt Options) (*App, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.json")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, opt), path
}

// doReq 发送请求并返回状态码、头部与响应体。
func doReq(app *App, method, target, body string) (int, http.Header, string) {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w.Code, w.Result().Header, w.Body.String()
}

// compact 把 JSON 文本规范化为紧凑形式以便比较。
func compact(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(strings.TrimSpace(s))); err != nil {
		t.Fatalf("测试数据规范化失败: %v (%s)", err, s)
	}
	return buf.String()
}

// arrayLen 读取数据文件中某个集合的记录数。
func arrayLen(t *testing.T, path, key string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(doc[key], &arr); err != nil {
		t.Fatal(err)
	}
	return len(arr)
}

// rawOf 读取响应体中指定字段（顶层对象）。
func rawOf(t *testing.T, body string, key string) json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("响应不是 JSON 对象: %v (%s)", err, body)
	}
	return doc[key]
}

type step struct {
	method, path, body string
	wantStatus         int
	wantBody           string // 空则跳过比较；否则紧凑 JSON 精确比较
	check              func(t *testing.T, body string)
}

func runSteps(t *testing.T, app *App, path string, steps []step) {
	t.Helper()
	for i, s := range steps {
		status, _, body := doReq(app, s.method, s.path, s.body)
		if status != s.wantStatus {
			t.Fatalf("步骤 %d %s %s: 状态码 %d, 期望 %d (body=%s)",
				i, s.method, s.path, status, s.wantStatus, body)
		}
		if s.wantBody != "" {
			if got, want := compact(t, body), compact(t, s.wantBody); got != want {
				t.Fatalf("步骤 %d %s %s: 响应体不符\n得到 %s\n期望 %s", i, s.method, s.path, got, want)
			}
		}
		if s.check != nil {
			s.check(t, body)
		}
	}
	_ = path
}

func TestCollectionCRUD(t *testing.T) {
	app, path := newTestApp(t, sampleDB, Options{})
	steps := []step{
		{method: "GET", path: "/posts", wantStatus: 200, wantBody: `
			[
			  { "id": 1, "title": "first post", "author": { "name": "alice" } },
			  { "id": 2, "title": "second post", "author": { "name": "bob" }, "views": 10 }
			]`},
		{method: "GET", path: "/posts/1", wantStatus: 200,
			wantBody: `{ "id": 1, "title": "first post", "author": { "name": "alice" } }`},
		{method: "GET", path: "/posts/1/", wantStatus: 200}, // 尾斜线等价
		{method: "GET", path: "/posts/99", wantStatus: 404,
			check: func(t *testing.T, body string) {
				if len(rawOf(t, body, "message")) == 0 {
					t.Fatal("404 响应应包含 message")
				}
			}},
		// 两个原版对空请求体都按 {} 处理（201）。
		{method: "POST", path: "/posts", body: ``, wantStatus: 201},
		{method: "POST", path: "/posts", body: `not json`, wantStatus: 400},
		{method: "POST", path: "/posts", body: `[1]`, wantStatus: 400},
		{method: "POST", path: "/posts", body: `{"id":1,"title":"dup"}`, wantStatus: 409},
		{method: "GET", path: "/posts", wantStatus: 200,
			check: func(t *testing.T, body string) {
				if n := arrayLen(t, path, "posts"); n != 3 {
					t.Fatalf("409 不应追加记录（前面空体 POST 已追加 1 条），应为 3 条，得到 %d", n)
				}
			}},
		{method: "POST", path: "/posts", body: `{"title":"new post"}`, wantStatus: 201,
			check: func(t *testing.T, body string) {
				if id := rawOf(t, body, "id"); string(id) == "" || string(id) == "null" {
					t.Fatalf("POST 响应应包含生成的 id: %s", body)
				}
				if n := arrayLen(t, path, "posts"); n != 4 {
					t.Fatalf("文件应追加记录，应为 4 条，得到 %d", n)
				}
			}},
		{method: "POST", path: "/posts", body: `{"id":"mine","title":"x"}`, wantStatus: 201},
		{method: "POST", path: "/posts", body: `{"title":"y"}`, wantStatus: 201},
		{method: "POST", path: "/posts", body: `{"id":1,"title":"z"}`, wantStatus: 409},
		{method: "PUT", path: "/posts/1", body: `{"id":999,"title":"replaced"}`, wantStatus: 200,
			wantBody: `{"id": 1, "title": "replaced"}`},
		{method: "GET", path: "/posts/1", wantStatus: 200, wantBody: `{"id":1,"title":"replaced"}`},
		{method: "PUT", path: "/posts/99", body: `{"title":"x"}`, wantStatus: 404},
		{method: "PUT", path: "/posts/1", body: `bad`, wantStatus: 400},
		{method: "PATCH", path: "/posts/2", body: `{"id":"ignored","views":11}`, wantStatus: 200,
			wantBody: `{"id": 2, "title": "second post", "author": { "name": "bob" }, "views": 11}`},
		{method: "PATCH", path: "/posts/99", body: `{"views":1}`, wantStatus: 404},
		{method: "DELETE", path: "/posts/2", wantStatus: 200,
			wantBody: `{"id": 2, "title": "second post", "author": { "name": "bob" }, "views": 11}`},
		{method: "DELETE", path: "/posts/2", wantStatus: 404},
	}
	runSteps(t, app, path, steps)
}

func TestSingleton(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	steps := []step{
		{method: "GET", path: "/profile", wantStatus: 200, wantBody: `{"name":"typicode"}`},
		{method: "PUT", path: "/profile", body: `{"name":"x","age":1}`, wantStatus: 200,
			wantBody: `{"name":"x","age":1}`},
		{method: "PATCH", path: "/profile", body: `{"age":2}`, wantStatus: 200,
			wantBody: `{"name":"x","age":2}`},
		{method: "GET", path: "/profile", wantStatus: 200, wantBody: `{"name":"x","age":2}`},
		{method: "DELETE", path: "/profile", wantStatus: 404},
		{method: "POST", path: "/profile", body: `{"a":1}`, wantStatus: 404},
		{method: "PUT", path: "/profile", body: `[1]`, wantStatus: 400},
	}
	runSteps(t, app, "", steps)
}

func TestDB(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	status, _, body := doReq(app, "GET", "/db", "")
	if status != 200 {
		t.Fatal(status)
	}
	if !strings.Contains(body, `"posts"`) || !strings.Contains(body, `"profile"`) {
		t.Fatalf("/db 应返回完整数据: %s", body)
	}
	// 键顺序应与数据文件一致
	if strings.Index(body, `"posts"`) > strings.Index(body, `"comments"`) {
		t.Fatalf("/db 应保持键顺序: %s", body)
	}
	if status, _, _ := doReq(app, "POST", "/db", `{}`); status != 404 {
		t.Fatal("POST /db 应 404")
	}
}

func TestNestedDependent(t *testing.T) {
	app, path := newTestApp(t, sampleDB, Options{})
	status, _, _ := doReq(app, "DELETE", "/posts/1?_dependent=comments", "")
	if status != 200 {
		t.Fatal(status)
	}
	if n := arrayLen(t, path, "comments"); n != 1 {
		t.Fatalf("_dependent 应级联删除 comments，应为 1 条，得到 %d", n)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), `"postId": 1`) {
		t.Fatalf("级联删除结果不符:\n%s", data)
	}
}

func TestReadOnly(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{ReadOnly: true})
	if status, _, _ := doReq(app, "POST", "/posts", `{"a":1}`); status != 403 {
		t.Fatalf("POST 应 403，得到 %d", status)
	}
	if status, _, _ := doReq(app, "GET", "/posts", ""); status != 200 {
		t.Fatalf("GET 应 200，得到 %d", status)
	}
}

func TestLegacyNumericID(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{Legacy: true})
	_, _, body := doReq(app, "POST", "/posts", `{"title":"x"}`)
	if id := rawOf(t, body, "id"); string(id) != "3" {
		t.Fatalf("--legacy 数字 id 应为现有最大值加一 (3)，得到 %s", id)
	}
	// 第二次的 id 应为 4（含第一次新增）
	_, _, body = doReq(app, "POST", "/posts", `{"title":"y"}`)
	if id := rawOf(t, body, "id"); string(id) != "4" {
		t.Fatalf("--legacy 数字 id 应为 4，得到 %s", id)
	}
}

func TestStringIDDefault(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	_, _, body := doReq(app, "POST", "/posts", `{"title":"x"}`)
	var id string
	if err := json.Unmarshal(rawOf(t, body, "id"), &id); err != nil {
		t.Fatalf("默认 id 应为字符串: %v (%s)", err, body)
	}
	if len(id) != 11 {
		t.Fatalf("id 应为 11 位随机串，得到 %q", id)
	}
}

func TestCustomIDField(t *testing.T) {
	data := `{"things": [ {"uid": 7, "name": "widget"} ]}`
	app, _ := newTestApp(t, data, Options{IDName: "uid"})
	if status, _, _ := doReq(app, "GET", "/things/7", ""); status != 200 {
		t.Fatalf("--id 自定义后应能命中，得到 %d", status)
	}
	_, _, body := doReq(app, "POST", "/things", `{"name":"new"}`)
	if len(rawOf(t, body, "uid")) == 0 {
		t.Fatalf("POST 应生成 uid: %s", body)
	}
}

func TestUnknownRoutes(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	// 注：GET / 返回资源首页（见 TestIndexPage），不是 404。
	for _, tc := range []struct{ method, path string }{
		{"GET", "/index.html"},
		{"GET", "/nonexistent"},
		{"POST", "/profile/x"},
		{"DELETE", "/nonexistent/1"},
		{"GET", "/profile/extra"},
	} {
		if status, _, _ := doReq(app, tc.method, tc.path, ""); status != 404 {
			t.Fatalf("%s %s 应 404，得到 %d", tc.method, tc.path, status)
		}
	}
}

func TestResponseHeader(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	_, h, _ := doReq(app, "GET", "/posts", "")
	if got := h.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type 应为 JSON: %q", got)
	}
}

func TestRequestLog(t *testing.T) {
	var buf bytes.Buffer
	app, _ := newTestApp(t, sampleDB, Options{Log: log.New(&buf, "", 0)})
	doReq(app, "GET", "/posts", "")
	out := buf.String()
	if !strings.Contains(out, "GET /posts 200") {
		t.Fatalf("请求日志应包含一行方法+路径+状态码: %q", out)
	}
}

func TestConcurrentReadAndWrite(t *testing.T) {
	app, path := newTestApp(t, `{"posts": []}`, Options{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if status, _, _ := doReq(app, "POST", "/posts", `{"n":1}`); status != 201 {
				t.Errorf("POST 应 201，得到 %d", status)
			}
		}(i)
		go func() {
			defer wg.Done()
			doReq(app, "GET", "/posts", "")
		}()
	}
	wg.Wait()
	if n := arrayLen(t, path, "posts"); n != 20 {
		t.Fatalf("并发写丢失: %d", n)
	}
}
