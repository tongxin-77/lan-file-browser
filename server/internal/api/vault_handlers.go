package api

import "net/http"

// 加密片库（04 文档）。配置见 config.Share.Vault。

// POST /api/vault/unlock —— 设备 token；始终 200（04 文档第 5 节）
func (s *Server) handleVaultUnlock(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// POST /api/vault/lock —— 设备 token + X-Vault
func (s *Server) handleVaultLock(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// PUT /api/admin/shares/{id}/vault —— 本机管理页：设为加密片库 / 修改密码（无需旧密码），Body {password}
func (s *Server) handleAdminSetVault(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// DELETE /api/admin/shares/{id}/vault —— 本机管理页：取消加密（删除 vault 字段）
func (s *Server) handleAdminDeleteVault(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }
