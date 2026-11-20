package api

import "net/http"

// 下一集与字幕（03 文档 4.6、4.7）。集号解析用 internal/episode（与整理库共用）。

// GET /api/next?share=&path=
func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// GET /api/subtitles?share=&path=
func (s *Server) handleSubtitles(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }
