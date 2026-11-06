// Package httpapi 实现 JsonSrv 的 REST 路由：集合与单例资源的增删改查、/db，
// 并预留查询语法、重写规则、静态文件与 CORS/gzip 的接入点。
// 目标是兼容 json-server v1，并在若干写法上始终兼容 v0.17。
package httpapi

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/PandaPanY/jsonsrv/internal/routes"
	"github.com/PandaPanY/jsonsrv/internal/store"
)

// Options 是 HTTP 层的行为配置。
type Options struct {
	IDName     string        // id 字段名，默认 id
	FKS        string        // 外键后缀，默认 Id
	ReadOnly   bool          // 写请求一律 403
	Legacy     bool          // v0.17 冲突行为开关（--legacy）
	Delay      time.Duration // --delay，模拟慢响应
	Log        *log.Logger   // 请求日志，nil 关闭
	Routes     []routes.Rule // --routes 重写规则
	StaticDirs []string      // --static 静态目录（按顺序查找）；空则尝试 ./public
	NoCORS     bool          // --no-cors 关闭 CORS
	NoGzip     bool          // --no-gzip 关闭 gzip
}

// App 实现 http.Handler，绑定一个 Store。
type App struct {
	st  *store.Store
	opt Options
}

// New 创建 App。
func New(st *store.Store, opt Options) *App {
	if opt.IDName == "" {
		opt.IDName = "id"
	}
	if opt.FKS == "" {
		opt.FKS = "Id"
	}
	return &App{st: st, opt: opt}
}

// ServeHTTP 实现 http.Handler。
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if !a.opt.NoCORS && applyCORS(w, r) {
		return // 预检请求已应答（预检不受 --delay 影响）
	}
	if a.opt.Delay > 0 {
		time.Sleep(a.opt.Delay)
	}
	// HEAD 按 GET 路由处理，响应头与 GET 一致但不写响应体；
	// 且不套 gzip（避免压缩尾帧使 Content-Length 不正确）。
	head := r.Method == http.MethodHead
	if head {
		r.Method = http.MethodGet
	}
	origURI := r.URL.RequestURI() // 日志固定用重写前的原始路径
	// 带 Range 的请求不压缩（静态文件返回 206 时压缩会破坏范围切送语义）。
	if !head && !a.opt.NoGzip && wantsGzip(r) && r.Header.Get("Range") == "" {
		gw := newGzipResponseWriter(w)
		// 响应体已写出，Close 只负责压缩尾帧，错误无从上报
		defer func() { _ = gw.Close() }()
		w = gw
	}
	// 0.17 实测：method-override 中间件（默认 methods=['POST']）对 POST 响应
	// 追加 Vary 令牌 X-HTTP-Method-Override（GET/PUT/PATCH/DELETE 均不含，
	// 即使请求不带该头也追加），且位于 Origin 与 Accept-Encoding 之间。
	// v1 无 method-override 中间件，故仅 --legacy 模式对齐此令牌。
	if a.opt.Legacy && r.Method == http.MethodPost {
		addVary(w.Header(), "X-HTTP-Method-Override")
	}
	// 0.17 实测：压缩中间件对所有非预检响应都追加 Vary: Accept-Encoding
	//（HEAD 亦含），用于对齐 Vary: Origin, Accept-Encoding。
	if !a.opt.NoGzip {
		addVary(w.Header(), "Accept-Encoding")
	}
	if head {
		w = &bodyLessWriter{ResponseWriter: w}
	}
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	a.route(rec, r)
	if a.opt.Log != nil {
		a.opt.Log.Printf("%s %s %d %s", r.Method, origURI,
			rec.status, time.Since(start).Round(time.Microsecond))
	}
}

// route 执行请求分派。
func (a *App) route(w http.ResponseWriter, r *http.Request) {
	if a.opt.ReadOnly && isWriteMethod(r.Method) {
		writeErr(w, http.StatusForbidden, "this server is in read-only mode")
		return
	}
	if len(a.opt.Routes) > 0 {
		if newSeg, newQ, ok := routes.Rewrite(a.opt.Routes, splitPath(r.URL.Path), r.URL.Query()); ok {
			u := *r.URL
			u.Path = "/" + strings.Join(newSeg, "/")
			u.RawPath = ""
			u.RawQuery = newQ.Encode()
			r.URL = &u
		}
	}
	seg := splitPath(r.URL.Path)
	if len(seg) == 0 {
		if a.staticOrIndex(w, r) {
			return
		}
		writeErr(w, http.StatusNotFound, "resource not found")
		return
	}
	if seg[0] == "db" {
		a.handleDB(w, r)
		return
	}
	snap := a.st.Snapshot()
	resID, rest := splitFirst(seg)
	res, isRes := a.resource(snap, resID)
	if !isRes {
		if a.staticOrIndex(w, r) {
			return
		}
		writeErr(w, http.StatusNotFound, "no resource named "+resID)
		return
	}
	switch {
	case len(rest) == 0:
		if res.singular {
			a.handleSingular(w, r, resID, res)
		} else {
			a.handleCollection(w, r, resID, res, "")
		}
	case len(rest) == 1 && !res.singular:
		a.handleCollection(w, r, resID, res, rest[0])
	default:
		// 嵌套路由（/posts/1/comments）
		a.handleNested(w, r, snap, resID, res, rest)
	}
}

// handleDB 返回整个数据库。
func (a *App) handleDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusNotFound, "unknown route")
		return
	}
	writeJSON(w, http.StatusOK, a.st.Snapshot())
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
