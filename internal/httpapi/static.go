package httpapi

// 静态文件与首页支持（--static / ./public / 资源清单首页）。

import (
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// staticOrIndex 处理静态文件与首页：
//   - 按 --static 目录顺序查找路径对应的文件（防目录穿越）；
//   - 未显式配置时，若 ./public 存在则作为默认目录；
//   - GET / 时优先返回目录下 index.html，否则生成资源清单首页。
//
// 返回值表示是否已处理该请求。
func (a *App) staticOrIndex(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	dirs := a.opt.StaticDirs
	if len(dirs) == 0 {
		if _, err := os.Stat("public"); err == nil {
			dirs = []string{"public"}
		}
	}

	rel := strings.TrimPrefix(r.URL.Path, "/")
	if rel == "" || strings.HasSuffix(r.URL.Path, "/") {
		rel += "index.html"
	}
	if file := findStatic(dirs, rel); file != "" {
		http.ServeFile(w, r, file)
		return true
	}

	// 根路径且没有 index.html：生成简洁首页。
	if r.URL.Path == "/" {
		a.writeIndexPage(w, r)
		return true
	}
	return false
}

// findStatic 在目录列表中按相对路径寻找普通文件（防 ../ 穿越）。
func findStatic(dirs []string, rel string) string {
	clean := filepath.ToSlash(filepath.Clean("/" + rel)) // 开头的 / 阻断 .. 穿越
	if clean == "/" {
		return ""
	}
	sub := strings.TrimPrefix(clean, "/")
	for _, dir := range dirs {
		p := filepath.Join(dir, filepath.FromSlash(sub))
		if !strings.HasPrefix(p, filepath.Clean(dir)+string(os.PathSeparator)) {
			continue // 防 .. 穿越出目录
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// writeIndexPage 输出简洁首页：列出资源名（带链接）与记录数。
func (a *App) writeIndexPage(w http.ResponseWriter, r *http.Request) {
	snap := a.st.Snapshot()
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n<head>\n<title>jsonsrv</title>\n")
	b.WriteString("<style>body{font-family:sans-serif;max-width:40em;margin:2em auto}")
	b.WriteString("a{color:#07c;text-decoration:none}a:hover{text-decoration:underline}")
	b.WriteString("table{border-collapse:collapse}td,th{padding:.3em .8em;border:1px solid #ddd}</style>\n</head>\n<body>\n")
	b.WriteString("<h1>jsonsrv</h1>\n<p>REST API over a data file. Available resources:</p>\n")
	b.WriteString("<table>\n<tr><th>Resource</th><th>Type</th><th>Records</th></tr>\n")
	for _, key := range snap.Keys() {
		val, _ := snap.Get(key)
		kind, count := "—", "—"
		if arr, ok := jsonx.AsArray(val); ok {
			kind, count = "collection", strconv.Itoa(len(arr))
		} else if _, ok := jsonx.AsObject(val); ok {
			kind, count = "singleton", "1"
		}
		b.WriteString("<tr><td><a href=\"/" + html.EscapeString(key) + "\">" +
			html.EscapeString(key) + "</a></td><td>" + kind + "</td><td>" + count + "</td></tr>\n")
	}
	b.WriteString("</table>\n</body>\n</html>\n")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, b.String())
}
