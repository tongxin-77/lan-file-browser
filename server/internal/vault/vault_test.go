package vault

import (
	"testing"
	"time"

	"lfb/server/internal/config"
)

var cheap = config.VaultParams{Time: 1, MemoryKiB: 64, Threads: 1, KeyLen: 32}

func TestHashVerify(t *testing.T) {
	v, err := Hash("1234", cheap)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enabled || v.Params != cheap || v.Salt == "" || v.PasswordHash == "" {
		t.Fatalf("vault 字段不全: %+v", v)
	}
	if !Verify(v, "1234") || Verify(v, "12345") || Verify(v, "") {
		t.Error("校验结果不对")
	}
	v2, _ := Hash("1234", cheap)
	if v2.Salt == v.Salt || v2.PasswordHash == v.PasswordHash {
		t.Error("每次哈希应使用新盐")
	}
	if Verify(&config.Vault{Enabled: true, Salt: "!!", PasswordHash: "x", Params: cheap}, "1234") {
		t.Error("损坏的配置不应通过")
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newSess() (*Sessions, *clock)   { c := &clock{time.Unix(1e9, 0)}; return NewSessions(c.now), c }

func TestSessionDeviceAndIdle(t *testing.T) {
	s, c := newSess()
	tok := s.Create("devA", map[string]string{"v1": "h1"})
	if len(tok) != 43 {
		t.Fatalf("令牌应为 32 字节 base64url: %q", tok)
	}
	if !s.Valid(tok, "devA", "v1", "h1") {
		t.Fatal("新会话应有效")
	}
	if s.Valid(tok, "devB", "v1", "h1") {
		t.Error("其他设备不能使用")
	}
	if s.Valid(tok, "devA", "v2", "h1") || s.Valid(tok, "devA", "v1", "h2") {
		t.Error("未解锁的片库 / 哈希变化（改密码）应无效")
	}
	if s.Valid("", "devA", "v1", "h1") {
		t.Error("空令牌应无效")
	}
	c.add(9 * time.Minute)
	if !s.Valid(tok, "devA", "v1", "h1") {
		t.Fatal("9 分钟内仍有效")
	}
	c.add(9 * time.Minute) // 使用会刷新空闲计时
	if !s.Valid(tok, "devA", "v1", "h1") {
		t.Fatal("刷新后 9 分钟仍有效")
	}
	c.add(Idle + time.Second)
	if s.Valid(tok, "devA", "v1", "h1") {
		t.Error("空闲 10 分钟应失效")
	}
}

func TestSessionLockAndDrop(t *testing.T) {
	s, _ := newSess()
	m := map[string]string{"v1": "h1"}
	a, b := s.Create("devA", m), s.Create("devB", m)
	s.Delete(a, "devB") // 别的设备不能销毁
	if !s.Valid(a, "devA", "v1", "h1") {
		t.Fatal("他人 lock 不应影响")
	}
	s.Delete(a, "devA")
	if s.Valid(a, "devA", "v1", "h1") {
		t.Error("lock 后应失效")
	}
	s.DropDevice("devB")
	if s.Valid(b, "devB", "v1", "h1") {
		t.Error("吊销设备后应失效")
	}
}

func TestRateLimit(t *testing.T) {
	s, c := newSess()
	for i := 0; i < 5; i++ {
		if !s.Attempt("devA") {
			t.Fatalf("第 %d 次应允许", i+1)
		}
	}
	if s.Attempt("devA") {
		t.Fatal("一分钟内第 6 次应限流")
	}
	if !s.Attempt("devB") {
		t.Fatal("限流按设备")
	}
	c.add(4 * time.Minute)
	if s.Attempt("devA") {
		t.Fatal("5 分钟内仍限流")
	}
	c.add(time.Minute + time.Second)
	if !s.Attempt("devA") {
		t.Fatal("5 分钟后恢复")
	}

	s2, c2 := newSess()
	for i := 0; i < 10; i++ { // 每 13 秒一次：任意一分钟内不超过 5 次
		if !s2.Attempt("d") {
			t.Fatalf("第 %d 次不应限流", i+1)
		}
		c2.add(13 * time.Second)
	}
}
