package api

import "net/http"

// 自动整理（06 文档第 11 节），全部仅本机。配置见 config.Share.Organize 与 config.Config.OrganizeDefaults；
// “播放时暂停”用 s.Activity.ActiveOn(卷)。

// GET /api/admin/shares/{id}/organize —— 配置 + 状态
func (s *Server) handleOrganizeGet(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// PUT /api/admin/shares/{id}/organize —— 整体保存配置
func (s *Server) handleOrganizePut(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// POST /api/admin/shares/{id}/organize/scan —— 立即扫描，202
func (s *Server) handleOrganizeScan(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// POST /api/admin/shares/{id}/organize/dry-run —— 试运行
func (s *Server) handleOrganizeDryRun(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// POST /api/admin/shares/{id}/organize/pause
func (s *Server) handleOrganizePause(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// POST /api/admin/shares/{id}/organize/resume
func (s *Server) handleOrganizeResume(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// GET /api/admin/shares/{id}/organize/items?state=&offset=&limit=
func (s *Server) handleOrganizeItems(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// POST /api/admin/shares/{id}/organize/items/retry —— Body {ids: []}
func (s *Server) handleOrganizeRetry(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// DELETE /api/admin/shares/{id}/organize/items/{itemId} —— 重新整理
func (s *Server) handleOrganizeDeleteItem(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, r)
}

// GET /api/admin/organize/defaults
func (s *Server) handleOrganizeDefaultsGet(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, r)
}

// PUT /api/admin/organize/defaults
func (s *Server) handleOrganizeDefaultsPut(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, r)
}

// POST /api/admin/organize/check-path —— Body {sharePath, sourcePath}
func (s *Server) handleOrganizeCheckPath(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, r)
}
