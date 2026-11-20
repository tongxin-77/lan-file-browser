// Package api 提供 HTTP 路由、鉴权中间件与统一错误格式。
package api

import (
	"database/sql"
	"net/http"
	"time"

	"lfb/server/internal/activity"
	"lfb/server/internal/config"
	"lfb/server/internal/web"
)

type Server struct {
	Config  *config.Store // 并发安全：读取用 Config.Get() 快照，修改用 Config.Update
	Auth    Authenticator
	Version string
	Port    int // 实际监听端口；0 = 取配置

	// P2 依赖（测试中可为 nil，使用前判空）
	Store    *sql.DB           // data.db，表由 internal/store 的迁移创建
	Activity *activity.Tracker // 活跃播放跟踪（05 文档 3.4）
}

// port 返回实际监听端口。
func (s *Server) port() int {
	if s.Port > 0 {
		return s.Port
	}
	return s.Config.Get().Port
}

// Handler 注册全部路由。处理函数见 auth_handlers.go、admin_handlers.go、file_handlers.go，
// P2 见 media_、progress_、thumb_、vault_、organize_handlers.go。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 连接（01 文档第 5 节）
	mux.HandleFunc("GET /api/ping", s.handlePing)  // 免鉴权
	mux.HandleFunc("POST /api/pair", s.handlePair) // 免鉴权，凭配对码
	mux.HandleFunc("GET /api/devices", localOnly(s.handleListDevices))
	mux.HandleFunc("DELETE /api/devices/{id}", s.adminWrite(s.handleDeleteDevice))

	// 本机管理页及其接口（仅本机；修改类另需 adminWrite 的 CSRF 校验）
	page := localOnly(web.Handler().ServeHTTP)
	mux.HandleFunc("GET /{$}", page)
	mux.HandleFunc("GET /app.js", page)
	mux.HandleFunc("GET /vault.js", page)
	mux.HandleFunc("GET /organize.js", page)
	mux.HandleFunc("GET /style.css", page)
	mux.HandleFunc("GET /api/admin/status", localOnly(s.handleAdminStatus))
	mux.HandleFunc("GET /api/admin/pairing", localOnly(s.handleAdminPairing))
	mux.HandleFunc("POST /api/admin/pairing/refresh", s.adminWrite(s.handleAdminPairingRefresh))
	mux.HandleFunc("GET /api/admin/shares", localOnly(s.handleAdminShares))
	mux.HandleFunc("POST /api/admin/shares", s.adminWrite(s.handleAdminCreateShare))
	mux.HandleFunc("PUT /api/admin/shares/{id}", s.adminWrite(s.handleAdminUpdateShare))
	mux.HandleFunc("DELETE /api/admin/shares/{id}", s.adminWrite(s.handleAdminDeleteShare))
	mux.HandleFunc("GET /api/admin/browse", localOnly(s.handleAdminBrowse))

	// 文件（03 文档 P1）
	mux.HandleFunc("GET /api/shares", s.requireDevice(s.handleShares))
	mux.HandleFunc("GET /api/list", s.requireDevice(s.handleList))
	mux.HandleFunc("GET /api/file", s.requireDevice(s.handleFile))
	mux.HandleFunc("GET /api/download", s.requireDevice(s.handleDownload))

	// 文件（03 文档 P2）
	mux.HandleFunc("GET /api/next", s.requireDevice(s.handleNext))
	mux.HandleFunc("GET /api/subtitles", s.requireDevice(s.handleSubtitles))
	mux.HandleFunc("POST /api/progress", s.requireDevice(s.handleProgress))
	mux.HandleFunc("GET /api/continue", s.requireDevice(s.handleContinue))
	mux.HandleFunc("GET /api/thumb", s.requireDevice(s.handleThumb))

	// 加密片库（04 文档）
	mux.HandleFunc("POST /api/vault/unlock", s.requireDevice(s.handleVaultUnlock))
	mux.HandleFunc("POST /api/vault/lock", s.requireDevice(s.handleVaultLock))
	mux.HandleFunc("PUT /api/admin/shares/{id}/vault", s.adminWrite(s.handleAdminSetVault))
	mux.HandleFunc("DELETE /api/admin/shares/{id}/vault", s.adminWrite(s.handleAdminDeleteVault))

	// 自动整理（06 文档第 11 节，仅本机）
	mux.HandleFunc("GET /api/admin/shares/{id}/organize", localOnly(s.handleOrganizeGet))
	mux.HandleFunc("PUT /api/admin/shares/{id}/organize", s.adminWrite(s.handleOrganizePut))
	mux.HandleFunc("POST /api/admin/shares/{id}/organize/scan", s.adminWrite(s.handleOrganizeScan))
	mux.HandleFunc("POST /api/admin/shares/{id}/organize/dry-run", s.adminWrite(s.handleOrganizeDryRun))
	mux.HandleFunc("POST /api/admin/shares/{id}/organize/pause", s.adminWrite(s.handleOrganizePause))
	mux.HandleFunc("POST /api/admin/shares/{id}/organize/resume", s.adminWrite(s.handleOrganizeResume))
	mux.HandleFunc("GET /api/admin/shares/{id}/organize/items", localOnly(s.handleOrganizeItems))
	mux.HandleFunc("POST /api/admin/shares/{id}/organize/items/retry", s.adminWrite(s.handleOrganizeRetry))
	mux.HandleFunc("DELETE /api/admin/shares/{id}/organize/items/{itemId}", s.adminWrite(s.handleOrganizeDeleteItem))
	mux.HandleFunc("GET /api/admin/organize/defaults", localOnly(s.handleOrganizeDefaultsGet))
	mux.HandleFunc("PUT /api/admin/organize/defaults", s.adminWrite(s.handleOrganizeDefaultsPut))
	mux.HandleFunc("POST /api/admin/organize/check-path", s.adminWrite(s.handleOrganizeCheckPath))

	return mux
}

// NewHTTPServer 不设全局 WriteTimeout（会切断长视频，见 05 文档 3.3），流式响应自行续期。
// ReadTimeout 只限制读取请求（头+体）：请求体读完后 net/http 会清除读期限，不影响响应写出。
func NewHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
