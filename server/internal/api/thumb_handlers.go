package api

import "net/http"

// 缩略图（03 文档 4.5）。已在 auth.Service 的 ?t= 白名单中。

// GET /api/thumb?share=&path=&w=
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }
