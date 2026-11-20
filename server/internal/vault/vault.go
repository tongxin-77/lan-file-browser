// Package vault 实现加密片库（04 文档）的密码哈希、解锁会话与解锁限流。
// 会话与限流状态只存内存，服务重启即全部失效。
package vault

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"lfb/server/internal/config"
)

// DefaultParams 为新设密码使用的 argon2id 参数（随哈希存入配置，见 04 文档第 3 节）。
var DefaultParams = config.VaultParams{Time: 3, MemoryKiB: 64 * 1024, Threads: 2, KeyLen: 32}

const (
	Idle        = 10 * time.Minute // 会话空闲失效
	maxAttempts = 5                // 每设备每分钟解锁次数
	window      = time.Minute
	blockFor    = 5 * time.Minute // 超限后一律返回空结果的时长
)

// Hash 用新的 16 字节随机盐计算 argon2id 哈希。
func Hash(password string, p config.VaultParams) (*config.Vault, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return &config.Vault{
		Enabled:      true,
		PasswordHash: base64.StdEncoding.EncodeToString(derive(password, salt, p)),
		Salt:         base64.StdEncoding.EncodeToString(salt),
		Params:       p,
	}, nil
}

// Verify 用 v 中保存的盐与参数校验密码（常量时间比较）。配置损坏时返回 false。
func Verify(v *config.Vault, password string) bool {
	salt, err1 := base64.StdEncoding.DecodeString(v.Salt)
	want, err2 := base64.StdEncoding.DecodeString(v.PasswordHash)
	if err1 != nil || err2 != nil || len(want) == 0 || v.Params.Time == 0 || v.Params.Threads == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(derive(password, salt, v.Params), want) == 1
}

// Dummy 做一次与 DefaultParams 等量的假校验（没有加密片库时补齐耗时）。
func Dummy(password string) {
	derive(password, make([]byte, 16), DefaultParams)
}

func derive(password string, salt []byte, p config.VaultParams) []byte {
	return argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, p.KeyLen)
}

type session struct {
	device string
	shares map[string]string // shareID → 解锁时的 PasswordHash；改密码 / 重设后不再匹配
	last   time.Time
}

type limit struct {
	attempts []time.Time
	blocked  time.Time // 此前一律限流
}

// Sessions 保存解锁会话与每设备的解锁限流状态，并发安全。
type Sessions struct {
	now func() time.Time

	mu       sync.Mutex
	sessions map[string]*session
	limits   map[string]*limit
}

func NewSessions(now func() time.Time) *Sessions {
	return &Sessions{now: now, sessions: map[string]*session{}, limits: map[string]*limit{}}
}

// Attempt 记录设备 dev 的一次解锁尝试；返回 false 表示被限流（不得校验密码）。
func (s *Sessions) Attempt(dev string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for d, l := range s.limits { // 顺带清理过期状态
		if now.After(l.blocked) && (len(l.attempts) == 0 || now.Sub(l.attempts[len(l.attempts)-1]) >= window) {
			delete(s.limits, d)
		}
	}
	l := s.limits[dev]
	if l == nil {
		l = &limit{}
		s.limits[dev] = l
	}
	if now.Before(l.blocked) {
		return false
	}
	keep := l.attempts[:0]
	for _, t := range l.attempts {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	l.attempts = keep
	if len(l.attempts) >= maxAttempts {
		l.attempts, l.blocked = nil, now.Add(blockFor)
		return false
	}
	l.attempts = append(l.attempts, now)
	return true
}

// Create 为设备 dev 新建会话，shares 为 shareID → 当前 PasswordHash。返回 32 字节随机令牌（base64url）。
func (s *Sessions) Create(dev string, shares map[string]string) string {
	b := make([]byte, 32)
	rand.Read(b) // 失败时 Go 运行时直接崩溃，不会返回错误
	tok := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, x := range s.sessions {
		if now.Sub(x.last) > Idle {
			delete(s.sessions, k)
		}
	}
	s.sessions[tok] = &session{dev, shares, now}
	return tok
}

// Valid 报告令牌 tok 是否为设备 dev 的有效会话且已解锁 shareID（哈希仍为 hash）；有效时刷新空闲计时。
func (s *Sessions) Valid(tok, dev, shareID, hash string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	x := s.sessions[tok]
	if x == nil || x.device != dev {
		return false
	}
	now := s.now()
	if now.Sub(x.last) > Idle {
		delete(s.sessions, tok)
		return false
	}
	h, ok := x.shares[shareID]
	if !ok || subtle.ConstantTimeCompare([]byte(h), []byte(hash)) != 1 {
		return false
	}
	x.last = now
	return true
}

// Delete 销毁设备 dev 的会话 tok（/api/vault/lock）；不属于 dev 时忽略。
func (s *Sessions) Delete(tok, dev string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x := s.sessions[tok]; x != nil && x.device == dev {
		delete(s.sessions, tok)
	}
}

// DropDevice 销毁设备的全部会话（设备被吊销）。
func (s *Sessions) DropDevice(dev string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, x := range s.sessions {
		if x.device == dev {
			delete(s.sessions, k)
		}
	}
}
