package api

import "net/http"

// 观看进度与继续观看（03 文档 4.8）。存储用 s.Store（data.db，表经 internal/store 迁移创建）；
// 收到进度上报时须调用 s.Activity.Progress(卷) 供阻止睡眠与整理库判断（05 文档 3.4）。

// POST /api/progress
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// GET /api/continue
func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }
