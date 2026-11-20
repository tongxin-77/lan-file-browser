package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lfb/server/internal/thumb"
)

// 缩略图（03 文档 4.5）。已在 auth.Service 的 ?t= 白名单中。

const defaultThumbWidth = 320

// GET /api/thumb?share=&path=&w=
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	width := defaultThumbWidth
	if v := q.Get("w"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, CodeBadPath, "参数非法")
			return
		}
		width = n
	}
	width = thumb.Width(width)

	sh := s.visibleShare(r)
	if sh == nil {
		writeError(w, CodeNotFound, "共享不存在")
		return
	}
	rel := q.Get("path")
	f, fi, err := openInShare(sh, rel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	defer f.Close()
	name := filepath.Base(filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/"))) // 用请求名判定类型
	if fi.IsDir() || !s.Thumbs.Can(name) {
		writeError(w, CodeNotFound, "无缩略图")
		return
	}
	src := thumb.Source{Name: name, RealPath: filepath.Join(sh.Path, filepath.FromSlash(rel)), ModTime: fi.ModTime(), Size: fi.Size(), File: f}
	p, err := s.Thumbs.Get(r.Context(), src, width)
	if err != nil {
		writeError(w, CodeNotFound, "无缩略图")
		return
	}
	cf, err := os.Open(p) // 缓存目录由服务端生成，不在共享内
	if err != nil {
		writeError(w, CodeNotFound, "无缩略图")
		return
	}
	defer cf.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", fi.ModTime(), cf)
}

// hasThumb 供 /api/list：图片恒为 true，视频仅在 ffmpeg 可用时为 true。
func (s *Server) hasThumb(name string) bool { return s.Thumbs.Can(name) }
