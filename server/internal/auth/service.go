package auth

import (
	"net/http"
	"strings"
	"time"
)

// Service 组合设备表与配对码，实现 api.Authenticator。
type Service struct {
	Store *Store
	Codes *Codes
}

func New(dataDir string, now func() time.Time) (*Service, error) {
	st, err := OpenStore(dataDir, now)
	if err != nil {
		return nil, err
	}
	return &Service{Store: st, Codes: NewCodes(now)}, nil
}

// DeviceFromRequest 从 Authorization: Bearer 取 token 并鉴权；?t= 仅限 GET（含 HEAD）
// /api/file、/api/download、/api/thumb（供播放器/图片组件直接使用 URL），其他路由只认请求头。
func (s *Service) DeviceFromRequest(r *http.Request) (string, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok && (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		(r.URL.Path == "/api/file" || r.URL.Path == "/api/download" || r.URL.Path == "/api/thumb") {
		tok = r.URL.Query().Get("t")
	}
	return s.Store.Lookup(tok)
}

// Pair 校验来自 ip 的配对码并为设备签发 token。错误为 ErrBadCode / ErrLocked / 存储错误。
func (s *Service) Pair(code, ip, deviceID, deviceName string) (string, error) {
	if err := s.Codes.Verify(code, ip); err != nil {
		return "", err
	}
	return s.Store.Add(deviceID, deviceName)
}
