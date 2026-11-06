package httpapi

// CORS 与 gzip：默认开启，--no-cors / --no-gzip 关闭。

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// 预检允许的方法（与 0.17 实测一致：不含 OPTIONS）。
const corsMethods = "GET,HEAD,PUT,PATCH,POST,DELETE"

// applyCORS 设置跨域响应头（默认与 --legacy 一致）：
// 带 Origin 时回显 Origin 并允许凭据；不带 Origin 时按 *。
// 始终带 Vary: Origin。原先按 Authorization 头判断凭证的逻辑已删除：
// Cookie 与预检请求都不带该头。v1 返回 * 且不回显凭证，此处默认模式与 v1
// 不同，属有意差异（前端带 Cookie 的联调需要）。
// 若请求是预检（OPTIONS + Access-Control-Request-Method），直接应答并返回 true。
func applyCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	h := w.Header()
	if origin != "" {
		h.Set("Access-Control-Allow-Origin", origin)
	} else {
		h.Set("Access-Control-Allow-Origin", "*")
	}
	h.Set("Access-Control-Allow-Credentials", "true")
	addVary(h, "Origin")

	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		h.Set("Access-Control-Allow-Methods", corsMethods)
		// 0.17 实测：回显 Access-Control-Request-Headers（无则不发该头），
		// 且 Vary 固定追加 Access-Control-Request-Headers。
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			h.Set("Access-Control-Allow-Headers", reqHeaders)
		}
		addVary(h, "Access-Control-Request-Headers")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// addVary 追加一个 Vary 令牌并合并为单个响应头值，
// 与原版（cors/compression 中间件）逐行形态一致，
// 避免 http.Header.Add 产生多行 Vary 被按行读到不同值。
func addVary(h http.Header, token string) {
	if v := h.Get("Vary"); v != "" {
		h.Set("Vary", v+", "+token)
	} else {
		h.Set("Vary", token)
	}
}

// bodyLessWriter 用于 HEAD 响应：响应头照常写出，响应体丢弃。
// 真实 net/http 会自行抑制 HEAD 的响应体，但 httptest.NewRecorder
// 不会，需在 handler 侧显式抑制，保证两种环境下都是空响应体。
type bodyLessWriter struct {
	http.ResponseWriter
}

func (b *bodyLessWriter) Write(p []byte) (int, error) { return len(p), nil }

// wantsGzip 报告客户端是否接受 gzip。
func wantsGzip(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip")
}

// gzipResponseWriter 对 JSON/文本响应启用 gzip 压缩。
type gzipResponseWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

// newGzipResponseWriter 包一层 gzip 写出器（按 Content-Type 决定）。
func newGzipResponseWriter(w http.ResponseWriter) *gzipResponseWriter {
	return &gzipResponseWriter{ResponseWriter: w}
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	ct := g.Header().Get("Content-Type")
	compressible := ct == "" || strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/")
	if code != http.StatusNoContent && code != http.StatusNotModified && compressible {
		g.Header().Set("Content-Encoding", "gzip")
		g.Header().Del("Content-Length")
		g.gz = gzip.NewWriter(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

// Close 结束 gzip 流。
func (g *gzipResponseWriter) Close() error {
	if g.gz == nil {
		return nil
	}
	return g.gz.Close()
}
