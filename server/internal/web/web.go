// Package web 内嵌本机管理页静态文件（原生 HTML/CSS/JS，无外部资源）。
package web

import (
	"embed"
	"net/http"
)

//go:embed static
var static embed.FS

// 路径 → 文件名与 Content-Type。显式指定类型：Windows 上 mime 包会读注册表，.js 可能被报成 text/plain。
var files = map[string][2]string{
	"/":            {"index.html", "text/html; charset=utf-8"},
	"/app.js":      {"app.js", "text/javascript; charset=utf-8"},
	"/vault.js":    {"vault.js", "text/javascript; charset=utf-8"},    // 加密片库设置（04）
	"/organize.js": {"organize.js", "text/javascript; charset=utf-8"}, // 自动整理（06）
	"/style.css":   {"style.css", "text/css; charset=utf-8"},
}

// Handler 输出管理页文件；调用方负责仅本机访问。
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err := static.ReadFile("static/" + f[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", f[1])
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY") // 防点击劫持
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
			"object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Write(data)
	})
}
