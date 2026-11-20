package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const (
	codeTTL       = 5 * time.Minute
	maxFails      = 5               // 同一 IP 连续失败上限
	lockDur       = 5 * time.Minute // IP 锁定时长；无新失败满此时长后计数也清零
	maxGlobalFail = 30              // globalWindow 内全体失败上限，达到即刷新 PIN
	globalWindow  = 5 * time.Minute
)

var (
	ErrBadCode = errors.New("配对码错误或已过期")
	ErrLocked  = errors.New("配对已锁定")
)

// ipFails 是单个来源 IP 的失败记录。
type ipFails struct {
	n           int
	last        time.Time
	lockedUntil time.Time
}

// Codes 管理 6 位 PIN 与二维码一次性码。两者均 5 分钟有效、过期自动刷新。
// 失败按来源 IP 计数：同一 IP 连续错 maxFails 次锁定 lockDur（锁定期间 PIN 一律拒绝）；
// 全体失败在 globalWindow 内达 maxGlobalFail 次则刷新 PIN（防分布式猜测）。
// 一次性码为 128 位随机数，不受锁定影响。Refresh 解除全部锁定。
type Codes struct {
	mu          sync.Mutex
	now         func() time.Time
	pin         string
	pinExp      time.Time
	code        string
	codeExp     time.Time
	ips         map[string]*ipFails
	lastPrune   time.Time
	globalFails int
	globalStart time.Time
}

func NewCodes(now func() time.Time) *Codes {
	c := &Codes{now: now}
	c.Refresh()
	return c
}

// Current 返回当前有效的 PIN 与一次性码（过期的先刷新）。
func (c *Codes) Current() (pin, code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renewExpired()
	return c.pin, c.code
}

// CurrentWithExpiry 同 Current，另返回 PIN 的过期时间（供本机管理页倒计时）。
func (c *Codes) CurrentWithExpiry() (pin, code string, pinExp time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renewExpired()
	return c.pin, c.code, c.pinExp
}

// Refresh 立即生成新的 PIN 与一次性码，并解除全部锁定。
func (c *Codes) Refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newPin()
	c.newCode()
	c.ips = map[string]*ipFails{}
	c.globalFails = 0
}

// Verify 校验来自 ip 的 PIN 或一次性码。一次性码成功后立即作废并换新。
func (c *Codes) Verify(input, ip string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renewExpired()
	now := c.now()
	c.prune(now)
	in := []byte(input)
	if input != "" && subtle.ConstantTimeCompare(in, []byte(c.code)) == 1 {
		c.newCode()
		delete(c.ips, ip)
		return nil
	}
	f := c.ips[ip]
	if f != nil && now.Before(f.lockedUntil) {
		return ErrLocked
	}
	if input != "" && subtle.ConstantTimeCompare(in, []byte(c.pin)) == 1 {
		delete(c.ips, ip)
		return nil
	}

	if f == nil || !now.Before(f.last.Add(lockDur)) {
		f = &ipFails{}
		c.ips[ip] = f
	}
	f.n++
	f.last = now
	if f.n >= maxFails {
		f.n, f.lockedUntil = 0, now.Add(lockDur)
	}
	if c.globalFails == 0 || !now.Before(c.globalStart.Add(globalWindow)) {
		c.globalFails, c.globalStart = 0, now
	}
	if c.globalFails++; c.globalFails >= maxGlobalFail {
		c.newPin()
		c.globalFails = 0
	}
	return ErrBadCode
}

// prune 每分钟至多一次，删除已解锁且 lockDur 内无新失败的 IP 记录，防计数表无限增长。
func (c *Codes) prune(now time.Time) {
	if now.Before(c.lastPrune.Add(time.Minute)) {
		return
	}
	c.lastPrune = now
	for ip, f := range c.ips {
		if !now.Before(f.lockedUntil) && !now.Before(f.last.Add(lockDur)) {
			delete(c.ips, ip)
		}
	}
}

func (c *Codes) renewExpired() {
	now := c.now()
	if !now.Before(c.pinExp) {
		c.newPin()
	}
	if !now.Before(c.codeExp) {
		c.newCode()
	}
}

func (c *Codes) newPin() {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	c.pin = fmt.Sprintf("%06d", n.Int64())
	c.pinExp = c.now().Add(codeTTL)
}

func (c *Codes) newCode() {
	b := make([]byte, 16)
	rand.Read(b)
	c.code = base64.RawURLEncoding.EncodeToString(b)
	c.codeExp = c.now().Add(codeTTL)
}
