package httpapi

import (
	"encoding/json"
	"net/http"
)

// statusRecorder 包装 ResponseWriter 以记录状态码。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// writeJSON 输出 JSON 响应（紧凑格式），状态码为 code。
func writeJSON(w http.ResponseWriter, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal serialization error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}

// writeErr 输出 {"message": "..."} 形式错误。
func writeErr(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"message": message})
}

// requestURL 返回"协议://请求 Host + 原始请求 URI"，与 0.17 get-full-url.js 一致。
func requestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.RequestURI()
}
