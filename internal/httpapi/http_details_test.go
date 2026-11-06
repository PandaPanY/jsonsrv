package httpapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// doReqH 带请求头的请求。
func doReqH(app *App, method, target, body string, headers map[string]string) (int, http.Header, string) {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	res := w.Result()
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return w.Code, res.Header, string(data)
}

// gunzip 解压 gzip 响应体。
func gunzip(t *testing.T, data string) string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(data))
	if err != nil {
		t.Fatalf("gzip 读取: %v", err)
	}
	defer func() { _ = zr.Close() }()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip 解压: %v", err)
	}
	return string(out)
}

func TestCORSDefaults(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	// 带 Origin（即使不带 Authorization）：回显 Origin + 允许凭据 + Vary: Origin。
	// 原先按 Authorization 判断凭证的行为已删除。
	_, head, _ := doReqH(app, "GET", "/posts", "", map[string]string{
		"Origin": "http://example.com", "Authorization": "Bearer x",
	})
	if head.Get("Access-Control-Allow-Origin") != "http://example.com" {
		t.Errorf("带 Origin 应回显: %q", head.Get("Access-Control-Allow-Origin"))
	}
	if head.Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("带 Origin 应允许凭据")
	}
	if head.Get("Vary") == "" || !strings.Contains(head.Get("Vary"), "Origin") {
		t.Errorf("应含 Vary: Origin: %q", head.Get("Vary"))
	}
	// 不带 Authorization 也必须回显（Cookie/预检场景不带该头）
	_, head, _ = doReqH(app, "GET", "/posts", "", map[string]string{"Origin": "http://example.com"})
	if head.Get("Access-Control-Allow-Origin") != "http://example.com" {
		t.Errorf("无 Authorization 也应回显 Origin: %q", head.Get("Access-Control-Allow-Origin"))
	}
	if head.Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("无 Authorization 也应允许凭据")
	}
	// 无 Origin：按 *，仍带 Vary: Origin
	_, head, _ = doReqH(app, "GET", "/posts", "", nil)
	if head.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("无 Origin 应为 *: %q", head.Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(head.Get("Vary"), "Origin") {
		t.Errorf("应始终带 Vary: Origin: %q", head.Get("Vary"))
	}
}

func TestCORSPreflight(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	status, head, _ := doReqH(app, "OPTIONS", "/posts", "", map[string]string{
		"Origin":                         "http://example.com",
		"Access-Control-Request-Method":  "PUT",
		"Access-Control-Request-Headers": "Content-Type",
	})
	if status != http.StatusNoContent {
		t.Fatalf("预检应为 204, 得到 %d", status)
	}
	if head.Get("Access-Control-Allow-Methods") != "GET,HEAD,PUT,PATCH,POST,DELETE" {
		t.Errorf("Allow-Methods: %q", head.Get("Access-Control-Allow-Methods"))
	}
	if head.Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Errorf("Allow-Headers 应原样回显: %q", head.Get("Access-Control-Allow-Headers"))
	}
	if head.Get("Access-Control-Max-Age") != "" {
		t.Errorf("0.17 实测无 Max-Age: %q", head.Get("Access-Control-Max-Age"))
	}
	if v := head.Get("Vary"); !strings.Contains(v, "Origin") || !strings.Contains(v, "Access-Control-Request-Headers") {
		t.Errorf("预检 Vary: %q", v)
	}
	// 无 Access-Control-Request-Headers：不回显 Allow-Headers（0.17 实测）
	_, head, _ = doReqH(app, "OPTIONS", "/posts", "", map[string]string{
		"Origin": "http://example.com", "Access-Control-Request-Method": "PATCH",
	})
	if head.Get("Access-Control-Allow-Headers") != "" {
		t.Errorf("无请求头列表时应不回显 Allow-Headers: %q", head.Get("Access-Control-Allow-Headers"))
	}
}

// TestCORSExpose 断言分页/切片/POST 201 的 Access-Control-Expose-Headers
// （与 0.17 plural.js/singular.js 实测对齐）。
func TestCORSExpose(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{Legacy: true})
	// 分页：X-Total-Count, Link
	_, head, _ := doReqH(app, "GET", "/posts?_page=1&_limit=1", "", map[string]string{"Origin": "http://example.com"})
	if head.Get("Access-Control-Expose-Headers") != "X-Total-Count, Link" {
		t.Errorf("分页 Expose: %q", head.Get("Access-Control-Expose-Headers"))
	}
	// 切片：X-Total-Count
	_, head, _ = doReqH(app, "GET", "/posts?_limit=1", "", map[string]string{"Origin": "http://example.com"})
	if head.Get("Access-Control-Expose-Headers") != "X-Total-Count" {
		t.Errorf("切片 Expose: %q", head.Get("Access-Control-Expose-Headers"))
	}
	// POST 201（legacy）：Expose: Location
	_, head, _ = doReqH(app, "POST", "/posts", `{"title":"x"}`, nil)
	if head.Get("Access-Control-Expose-Headers") != "Location" {
		t.Errorf("POST 201 Expose: %q", head.Get("Access-Control-Expose-Headers"))
	}
	// 非分页/切片的普通 GET 不发 Expose
	_, head, _ = doReqH(app, "GET", "/posts/1", "", nil)
	if head.Get("Access-Control-Expose-Headers") != "" {
		t.Errorf("普通 GET 不应发 Expose: %q", head.Get("Access-Control-Expose-Headers"))
	}
}

func TestNoCORS(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{NoCORS: true})
	_, head, _ := doReqH(app, "GET", "/posts", "", map[string]string{"Origin": "http://example.com"})
	if head.Get("Access-Control-Allow-Origin") != "" {
		t.Error("--no-cors 不应发 CORS 头")
	}
}

func TestGzip(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	// 客户端接受 gzip：JSON 响应压缩
	_, head, body := doReqH(app, "GET", "/posts", "", map[string]string{"Accept-Encoding": "gzip"})
	if head.Get("Content-Encoding") != "gzip" {
		t.Fatalf("应 gzip: %v", head)
	}
	if !strings.Contains(gunzip(t, body), `"title":"first post"`) {
		t.Errorf("解压内容不符: %s", gunzip(t, body))
	}
	// 不接受 gzip：原样
	_, head, body = doReqH(app, "GET", "/posts", "", nil)
	if head.Get("Content-Encoding") != "" {
		t.Errorf("不应 gzip: %v", head)
	}
	if !strings.Contains(body, "first post") {
		t.Errorf("明文响应不符: %s", body)
	}
	// --no-gzip 关闭
	app2, _ := newTestApp(t, sampleDB, Options{NoGzip: true})
	_, head, _ = doReqH(app2, "GET", "/posts", "", map[string]string{"Accept-Encoding": "gzip"})
	if head.Get("Content-Encoding") != "" {
		t.Error("--no-gzip 不应压缩")
	}
}

func TestStaticAndIndex(t *testing.T) {
	pub := t.TempDir()
	if err := os.WriteFile(filepath.Join(pub, "hello.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pub, "index.html"), []byte("<h1>index</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	app, _ := newTestApp(t, sampleDB, Options{StaticDirs: []string{pub}})

	// 静态文件
	status, head, body := doReqH(app, "GET", "/hello.html", "", nil)
	if status != 200 || body != "<h1>hi</h1>" {
		t.Errorf("静态文件: %d %q", status, body)
	}
	if !strings.Contains(head.Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type: %q", head.Get("Content-Type"))
	}
	// / 返回 index.html
	if _, _, body = doReqH(app, "GET", "/", "", nil); !strings.Contains(body, "index") {
		t.Errorf("index.html 优先: %s", body)
	}
	// 资源优先：GET /posts 不被静态吞掉
	if status, _, body = doReqH(app, "GET", "/posts", "", nil); status != 200 || !strings.Contains(body, "first post") {
		t.Errorf("资源路由优先: %d %s", status, body)
	}
	// 目录穿越防护
	wd := t.TempDir()
	if err := os.WriteFile(wd+"/secret", []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _, _ = doReqH(app, "GET", "/"+filepath.Base(wd)+"/secret", "", nil)
	if status == 200 {
		t.Error("目录外文件不应被当作静态资源")
	}
}

func TestIndexPage(t *testing.T) {
	// 无静态目录时 GET / 返回资源首页
	app, _ := newTestApp(t, sampleDB, Options{StaticDirs: []string{t.TempDir()}})
	status, head, body := doReqH(app, "GET", "/", "", nil)
	if status != 200 {
		t.Fatalf("首页状态 %d", status)
	}
	if !strings.Contains(head.Get("Content-Type"), "text/html") {
		t.Errorf("首页类型: %q", head.Get("Content-Type"))
	}
	for _, want := range []string{"posts", "comments", "profile"} {
		if !strings.Contains(body, want) {
			t.Errorf("首页应列出 %s: %s", want, body)
		}
	}
}

func TestReadOnlyDelay(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{ReadOnly: true, Delay: 10 * time.Millisecond})
	start := time.Now()
	status, _, _ := doReq(app, "POST", "/posts", `{"title":"x"}`)
	elapsed := time.Since(start)
	if status != http.StatusForbidden {
		t.Errorf("只读应 403, 得到 %d", status)
	}
	if elapsed < 10*time.Millisecond {
		t.Errorf("--delay 应至少 10ms, 得到 %v", elapsed)
	}
}

// TestLegacyMethodOverrideVary 对照 0.17 实测：
// method-override 中间件仅对 POST 响应追加 Vary 令牌 X-HTTP-Method-Override，
// 位置在 Origin 与 Accept-Encoding 之间；GET/PUT/PATCH/DELETE 均不含该令牌。
// v1（默认模式）无该中间件，POST 也不追加。
func TestLegacyMethodOverrideVary(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{Legacy: true})
	_, head, _ := doReqH(app, "POST", "/posts", `{"title":"t"}`, nil)
	if v := head.Get("Vary"); v != "Origin, X-HTTP-Method-Override, Accept-Encoding" {
		t.Errorf("legacy POST Vary: %q", v)
	}
	_, head, _ = doReqH(app, "PATCH", "/posts/1", `{"title":"t"}`, nil)
	if v := head.Get("Vary"); v != "Origin, Accept-Encoding" {
		t.Errorf("legacy PATCH 不应含 method-override 令牌: %q", v)
	}
	// 非 legacy（v1 对照模式）POST 无 method-override 中间件。
	appV1, _ := newTestApp(t, sampleDB, Options{})
	_, head, _ = doReqH(appV1, "POST", "/posts", `{"title":"t"}`, nil)
	if v := head.Get("Vary"); v != "Origin, Accept-Encoding" {
		t.Errorf("默认模式 POST 不应含 method-override 令牌: %q", v)
	}
}
