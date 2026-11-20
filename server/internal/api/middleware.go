package api

import (
	"context"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Authenticator 从请求（Authorization: Bearer 或 ?t=）中识别已配对设备。
type Authenticator interface {
	DeviceFromRequest(r *http.Request) (deviceID string, ok bool)
}

type ctxKey struct{}

// DeviceID 返回鉴权中间件放入 context 的设备 ID。
func DeviceID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// requireDevice 要求有效 token，并把 deviceID 放入 context。
func (s *Server) requireDevice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.Auth.DeviceFromRequest(r)
		if !ok {
			writeError(w, CodeUnauthorized, "未授权，请重新配对")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	}
}

// localOnly 仅允许本机（127.0.0.1 / ::1）访问：看 TCP 远端地址（不信任转发头），
// 并要求 Host 为本机名，防 DNS 重绑定（本机浏览器访问恶意域名解析到 127.0.0.1）。
func localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() || !localHostHeader(r.Host) {
			writeError(w, CodeForbidden, "仅限本机访问")
			return
		}
		next(w, r)
	}
}

// adminWrite 用于本机管理接口的修改类请求（POST/PUT/DELETE）：在 localOnly 之上防 CSRF。
// Host 校验挡不住恶意网页让本机浏览器向 http://127.0.0.1:端口 发表单 POST，故另要求：
// 自定义头 X-LFB-Admin: 1 与 Content-Type: application/json（跨站请求带这两者必须先过 CORS 预检，
// 而本服务不响应预检）；若带 Origin，必须是本机管理页自身的源。
func (s *Server) adminWrite(next http.HandlerFunc) http.HandlerFunc {
	return localOnly(func(w http.ResponseWriter, r *http.Request) {
		if !s.adminRequestOK(r) {
			writeError(w, CodeForbidden, "请求来源不是本机管理页")
			return
		}
		next(w, r)
	})
}

func (s *Server) adminRequestOK(r *http.Request) bool {
	if r.Header.Get("X-LFB-Admin") != "1" {
		return false
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		p := strconv.Itoa(s.port())
		return o == "http://127.0.0.1:"+p || o == "http://localhost:"+p || o == "http://[::1]:"+p
	}
	return true
}

// localHostHeader 报告 Host 头（可带端口，大小写不敏感）是否为 127.0.0.1、localhost 或 [::1]。
func localHostHeader(h string) bool {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	switch strings.ToLower(h) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// TokenMap 是 token → deviceID 的简单实现，供测试及鉴权完成前占位使用。
type TokenMap map[string]string

func (m TokenMap) DeviceFromRequest(r *http.Request) (string, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		tok = r.URL.Query().Get("t")
	}
	id, ok := m[tok]
	return id, ok && tok != ""
}
