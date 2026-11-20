package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"lfb/server/internal/config"
	"lfb/server/internal/vault"
)

// 加密片库（04 文档）。配置见 config.Share.Vault，哈希与会话见 internal/vault。

const unlockMinDuration = 800 * time.Millisecond // 解锁响应的固定最短耗时

// vaultState 懒创建会话表；测试可预先填入（假时钟、短耗时）。
type vaultState struct {
	mu       sync.Mutex
	sessions *vault.Sessions
	delay    time.Duration // 0 = unlockMinDuration
}

func (s *Server) vaultSessions() (*vault.Sessions, time.Duration) {
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.sessions == nil {
		s.vault.sessions = vault.NewSessions(time.Now)
	}
	if s.vault.delay == 0 {
		s.vault.delay = unlockMinDuration
	}
	return s.vault.sessions, s.vault.delay
}

func isVault(sh *config.Share) bool { return sh.Vault != nil && sh.Vault.Enabled }

// vaultToken 取请求中的解锁会话：X-Vault 头；?v= 仅限 GET/HEAD 的 /api/file、/api/download、/api/thumb
// （与 ?t= 白名单一致，见 auth.Service.DeviceFromRequest）。
func vaultToken(r *http.Request) string {
	if v := r.Header.Get("X-Vault"); v != "" {
		return v
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		(r.URL.Path == "/api/file" || r.URL.Path == "/api/download" || r.URL.Path == "/api/thumb") {
		return r.URL.Query().Get("v")
	}
	return ""
}

// accessible 报告共享对当前请求是否存在：对设备可见，且不是加密片库或带有本设备的有效会话（04 文档第 6 节）。
func (s *Server) accessible(r *http.Request, sh *config.Share) bool {
	dev := DeviceID(r.Context())
	if !visible(sh, dev) {
		return false
	}
	if !isVault(sh) {
		return true
	}
	sess, _ := s.vaultSessions()
	return sess.Valid(vaultToken(r), dev, sh.ID, sh.Vault.PasswordHash)
}

// POST /api/vault/unlock {password} —— 始终 200；错密码 / 无加密片库 / 被限流响应完全相同，
// 且补齐到固定最短耗时（04 文档第 5 节）。
func (s *Server) handleVaultUnlock(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sess, delay := s.vaultSessions()
	dev := DeviceID(r.Context())
	var in struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) // 非法请求体按错密码处理

	matched := map[string]string{}
	items := []shareItem{}
	if sess.Attempt(dev) {
		n := 0
		for _, sh := range s.allShares() {
			if !isVault(&sh) {
				continue
			}
			n++ // 对所有加密片库逐个校验，耗时不随可见性变化
			if vault.Verify(sh.Vault, in.Password) && visible(&sh, dev) {
				matched[sh.ID] = sh.Vault.PasswordHash
				items = append(items, toShareItem(&sh))
			}
		}
		if n == 0 {
			vault.Dummy(in.Password)
		}
	}
	var token *string
	if len(matched) > 0 {
		t := sess.Create(dev, matched)
		token = &t
	}
	time.Sleep(time.Until(start.Add(delay)))
	writeJSON(w, map[string]any{"session": token, "expiresIn": int(vault.Idle / time.Second), "shares": items})
}

// POST /api/vault/lock —— 设备 token + X-Vault；立即销毁会话，始终 200
func (s *Server) handleVaultLock(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.vaultSessions()
	sess.Delete(r.Header.Get("X-Vault"), DeviceID(r.Context()))
	writeJSON(w, map[string]any{})
}

// PUT /api/admin/shares/{id}/vault —— 本机管理页：设为加密片库 / 修改密码（无需旧密码），Body {password}
func (s *Server) handleAdminSetVault(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	if n := utf8.RuneCountInString(in.Password); n < 4 || n > 16 {
		writeError(w, CodeBadPath, "密码要 4 到 16 位")
		return
	}
	if s.Config.Get().Share(r.PathValue("id")) == nil { // 先查一次，避免为不存在的片库做哈希
		writeError(w, CodeNotFound, "片库不存在")
		return
	}
	v, err := vault.Hash(in.Password, vault.DefaultParams) // 新盐 → 新哈希，旧会话随之失效
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	s.updateVault(w, r.PathValue("id"), v)
}

// DELETE /api/admin/shares/{id}/vault —— 本机管理页：取消加密（删除 vault 字段）
func (s *Server) handleAdminDeleteVault(w http.ResponseWriter, r *http.Request) {
	s.updateVault(w, r.PathValue("id"), nil)
}

func (s *Server) updateVault(w http.ResponseWriter, id string, v *config.Vault) {
	var updated config.Share
	err := s.Config.Update(func(c *config.Config) error {
		sh := c.Share(id)
		if sh == nil {
			return &adminErr{CodeNotFound, "片库不存在"}
		}
		sh.Vault = v
		updated = *sh
		return nil
	})
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	writeJSON(w, toAdminShare(updated))
}
