package api

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"lfb/server/internal/auth"
)

// authService 返回真实鉴权服务；占位 Authenticator（如 TokenMap）时配对接口保持 501。
func (s *Server) authService(w http.ResponseWriter, r *http.Request) (*auth.Service, bool) {
	svc, ok := s.Auth.(*auth.Service)
	if !ok {
		notImplemented(w, r)
	}
	return svc, ok
}

func writeAuthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// GET /api/ping —— 免鉴权，返回 {id, name, version, needPair}
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.authService(w, r)
	if !ok {
		return
	}
	_, valid := svc.DeviceFromRequest(r)
	cfg := s.Config.Get()
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"id": cfg.ServerID, "name": cfg.Name, "version": s.Version, "needPair": !valid,
	})
}

// POST /api/pair —— 免鉴权，凭配对码换取 token
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.authService(w, r)
	if !ok {
		return
	}
	var req struct{ Code, DeviceName, DeviceID string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil ||
		req.DeviceID == "" || len(req.DeviceID) > 128 || len(req.DeviceName) > 128 {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	tok, err := svc.Pair(req.Code, ip, req.DeviceID, req.DeviceName)
	switch {
	case errors.Is(err, auth.ErrLocked):
		writeError(w, CodeLocked, "错误次数过多，请 5 分钟后重试或扫码配对")
	case errors.Is(err, auth.ErrBadCode):
		writeError(w, CodeUnauthorized, "配对码错误或已过期")
	case err != nil:
		log.Printf("配对失败: %v", err)
		writeError(w, CodeInternal, "服务端错误")
	default:
		log.Printf("设备已配对: %q (%q)", req.DeviceName, req.DeviceID) // %q 转义控制字符，防日志注入
		cfg := s.Config.Get()
		writeAuthJSON(w, http.StatusOK, map[string]string{
			"token": tok, "serverId": cfg.ServerID, "name": cfg.Name,
		})
	}
}

// GET /api/devices —— 仅本机
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.authService(w, r)
	if !ok {
		return
	}
	type device struct {
		DeviceID   string    `json:"deviceId"`
		DeviceName string    `json:"deviceName"`
		CreatedAt  time.Time `json:"createdAt"`
		LastSeen   time.Time `json:"lastSeen"`
	}
	list := []device{}
	for _, d := range svc.Store.List() {
		list = append(list, device{d.DeviceID, d.DeviceName, d.CreatedAt, d.LastSeen})
	}
	writeAuthJSON(w, http.StatusOK, list)
}

// DELETE /api/devices/{id} —— 仅本机，吊销设备
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.authService(w, r)
	if !ok {
		return
	}
	found, err := svc.Store.Revoke(r.PathValue("id"))
	switch {
	case err != nil:
		log.Printf("吊销设备失败: %v", err)
		writeError(w, CodeInternal, "服务端错误")
	case !found:
		writeError(w, CodeNotFound, "设备不存在")
	default:
		sess, _ := s.vaultSessions()
		sess.DropDevice(r.PathValue("id")) // 吊销设备即销毁其解锁会话（04 文档第 5 节）
		w.WriteHeader(http.StatusNoContent)
	}
}
