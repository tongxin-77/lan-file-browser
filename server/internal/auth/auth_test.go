package auth

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)} }
func mustStore(t *testing.T, dir string, c *clock) *Store {
	t.Helper()
	s, err := OpenStore(dir, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreAddLookupReload(t *testing.T) {
	dir, c := t.TempDir(), newClock()
	s := mustStore(t, dir, c)
	tok, err := s.Add("dev1", "Pixel 8")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(tok) {
		t.Fatalf("token 应为 32 字节 base64url: %q", tok)
	}
	if id, ok := s.Lookup(tok); !ok || id != "dev1" {
		t.Fatalf("Lookup: %q %v", id, ok)
	}
	if _, ok := s.Lookup("bad"); ok {
		t.Fatal("错误 token 不应通过")
	}
	if _, ok := s.Lookup(""); ok {
		t.Fatal("空 token 不应通过")
	}

	data, _ := os.ReadFile(filepath.Join(dir, "devices.json"))
	if strings.Contains(string(data), tok) || !strings.Contains(string(data), `"tokenHash": "sha256:`) {
		t.Fatalf("devices.json 应只存哈希: %s", data)
	}

	s2 := mustStore(t, dir, c)
	if id, ok := s2.Lookup(tok); !ok || id != "dev1" {
		t.Fatal("重载后 token 应仍有效")
	}
}

func TestStoreOverwriteAndRevoke(t *testing.T) {
	dir, c := t.TempDir(), newClock()
	s := mustStore(t, dir, c)
	old, _ := s.Add("dev1", "A")
	tok, _ := s.Add("dev1", "B")
	if _, ok := s.Lookup(old); ok {
		t.Fatal("重新配对后旧 token 应失效")
	}
	if l := s.List(); len(l) != 1 || l[0].DeviceName != "B" {
		t.Fatalf("同一 deviceId 应覆盖: %+v", l)
	}
	if ok, err := s.Revoke("dev1"); !ok || err != nil {
		t.Fatalf("Revoke: %v %v", ok, err)
	}
	if _, ok := s.Lookup(tok); ok {
		t.Fatal("吊销后 token 应失效")
	}
	if ok, _ := s.Revoke("dev1"); ok {
		t.Fatal("重复吊销应返回 false")
	}
	if len(mustStore(t, dir, c).List()) != 0 {
		t.Fatal("吊销应落盘")
	}
}

func TestStoreLastSeenThrottle(t *testing.T) {
	dir, c := t.TempDir(), newClock()
	s := mustStore(t, dir, c)
	tok, _ := s.Add("dev1", "A")
	start := c.t

	c.add(30 * time.Second)
	s.Lookup(tok)
	if got := mustStore(t, dir, c).List()[0].LastSeen; !got.Equal(start) {
		t.Fatalf("1 分钟内不应更新落盘: %v", got)
	}
	c.add(31 * time.Second)
	s.Lookup(tok)
	if got := mustStore(t, dir, c).List()[0].LastSeen; !got.Equal(c.t) {
		t.Fatalf("超过 1 分钟应更新: %v want %v", got, c.t)
	}
}

func TestCodesExpiry(t *testing.T) {
	c := newClock()
	k := NewCodes(c.now)
	pin, code := k.Current()
	if !regexp.MustCompile(`^\d{6}$`).MatchString(pin) || len(code) < 20 {
		t.Fatalf("pin=%q code=%q", pin, code)
	}
	c.add(5*time.Minute + time.Second)
	if err := k.Verify(pin, ipA); !errors.Is(err, ErrBadCode) {
		t.Fatalf("过期 PIN 应失败: %v", err)
	}
	if err := k.Verify(code, ipA); !errors.Is(err, ErrBadCode) {
		t.Fatalf("过期一次性码应失败: %v", err)
	}
	pin2, code2 := k.Current()
	if code2 == code {
		t.Fatal("过期后应自动刷新")
	}
	_ = pin2 // PIN 可能巧合相同，不断言
}

func TestCodesOneTime(t *testing.T) {
	k := NewCodes(newClock().now)
	pin, code := k.Current()
	if err := k.Verify(code, ipA); err != nil {
		t.Fatal(err)
	}
	if err := k.Verify(code, ipA); !errors.Is(err, ErrBadCode) {
		t.Fatalf("一次性码用后应作废: %v", err)
	}
	if _, c2 := k.Current(); c2 == code {
		t.Fatal("用后应生成新码")
	}
	if err := k.Verify(pin, ipA); err != nil {
		t.Fatalf("PIN 在有效期内可重复使用: %v", err)
	}
}

const ipA, ipB = "192.168.1.9", "192.168.1.10"

func TestCodesLock(t *testing.T) {
	c := newClock()
	k := NewCodes(c.now)
	pin, _ := k.Current()
	for i := 0; i < 4; i++ {
		k.Verify("000000x", ipA)
	}
	if err := k.Verify(pin, ipA); err != nil {
		t.Fatal("成功应清零计数")
	}
	for i := 0; i < 5; i++ {
		if err := k.Verify("bad", ipA); !errors.Is(err, ErrBadCode) {
			t.Fatalf("第 %d 次: %v", i+1, err)
		}
	}
	if err := k.Verify(pin, ipA); !errors.Is(err, ErrLocked) {
		t.Fatalf("连续错 5 次后应锁定该 IP: %v", err)
	}
	k.Refresh()
	pin, _ = k.Current()
	if err := k.Verify(pin, ipA); err != nil {
		t.Fatalf("手动刷新后应解锁: %v", err)
	}
}

func TestCodesLockPerIP(t *testing.T) {
	c := newClock()
	k := NewCodes(c.now)
	for i := 0; i < 5; i++ {
		k.Verify("bad", ipA)
	}
	pin, code := k.Current()
	if err := k.Verify(pin, ipA); !errors.Is(err, ErrLocked) {
		t.Fatalf("IP-A 应锁定: %v", err)
	}
	if err := k.Verify(pin, ipB); err != nil {
		t.Fatalf("IP-A 锁定不应影响 IP-B: %v", err)
	}
	if err := k.Verify(code, ipA); err != nil {
		t.Fatalf("一次性码不受锁定影响: %v", err)
	}
	// 一次性码成功会清零 IP-A，重新锁定后验证时间解锁
	for i := 0; i < 5; i++ {
		k.Verify("bad", ipA)
	}
	c.add(5*time.Minute - time.Second)
	pin, _ = k.Current()
	if err := k.Verify(pin, ipA); !errors.Is(err, ErrLocked) {
		t.Fatalf("5 分钟内仍应锁定: %v", err)
	}
	c.add(time.Second)
	pin, _ = k.Current() // PIN 同时到期刷新
	if err := k.Verify(pin, ipA); err != nil {
		t.Fatalf("5 分钟后应自动解锁: %v", err)
	}
}

func TestCodesGlobalPinLimit(t *testing.T) {
	c := newClock()
	k := NewCodes(c.now)
	pin, code := k.Current()
	for i := 0; i < 29; i++ {
		k.Verify("bad", fmt.Sprintf("10.0.%d.1", i)) // 分布式猜测，每 IP 不足 5 次
	}
	if p, _ := k.Current(); p != pin {
		t.Fatal("未达全局上限不应刷新 PIN")
	}
	k.Verify("bad", "10.1.0.1")
	if err := k.Verify(pin, ipB); !errors.Is(err, ErrBadCode) {
		t.Fatalf("累计 30 次失败后旧 PIN 应作废: %v", err)
	}
	if err := k.Verify(code, ipB); err != nil {
		t.Fatalf("一次性码不受影响: %v", err)
	}
	pin2, _ := k.Current()
	if err := k.Verify(pin2, ipB); err != nil {
		t.Fatalf("新 PIN 应可用: %v", err)
	}

	// 失败分散在超过 5 分钟的窗口中不触发
	k = NewCodes(c.now)
	for i := 0; i < 29; i++ {
		k.Verify("bad", fmt.Sprintf("10.2.%d.1", i))
	}
	c.add(5 * time.Minute)
	k.Verify("bad", "10.3.0.1")
	if k.globalFails != 1 {
		t.Fatalf("窗口过期后应重新计数: %d", k.globalFails)
	}
}

func TestCodesPruneIPs(t *testing.T) {
	c := newClock()
	k := NewCodes(c.now)
	for i := 0; i < 100; i++ {
		k.Verify("bad", fmt.Sprintf("10.0.%d.1", i))
	}
	if len(k.ips) != 100 {
		t.Fatalf("应记录 100 个 IP: %d", len(k.ips))
	}
	c.add(5*time.Minute + time.Second)
	k.Verify("bad", ipA)
	if len(k.ips) != 1 {
		t.Fatalf("过期条目应被清理: %d", len(k.ips))
	}
}

func TestServiceDeviceFromRequest(t *testing.T) {
	c := newClock()
	svc, err := New(t.TempDir(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	pin, _ := svc.Codes.Current()
	tok, err := svc.Pair(pin, ipA, "dev1", "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/list", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if id, ok := svc.DeviceFromRequest(r); !ok || id != "dev1" {
		t.Fatal("Bearer 方式失败")
	}
	r = httptest.NewRequest("GET", "/api/file?t="+tok, nil)
	if id, ok := svc.DeviceFromRequest(r); !ok || id != "dev1" {
		t.Fatal("?t= 方式失败")
	}
	r = httptest.NewRequest("GET", "/api/download?t="+tok, nil)
	if id, ok := svc.DeviceFromRequest(r); !ok || id != "dev1" {
		t.Fatal("download ?t= 方式失败")
	}
	r = httptest.NewRequest("GET", "/api/thumb?t="+tok, nil)
	if id, ok := svc.DeviceFromRequest(r); !ok || id != "dev1" {
		t.Fatal("thumb ?t= 方式失败")
	}
	for _, u := range []string{"/api/shares", "/api/list", "/api/ping", "/api/devices", "/api/next", "/api/subtitles"} {
		if _, ok := svc.DeviceFromRequest(httptest.NewRequest("GET", u+"?t="+tok, nil)); ok {
			t.Fatalf("%s 不应接受 ?t=", u)
		}
	}
	if _, ok := svc.DeviceFromRequest(httptest.NewRequest("POST", "/api/file?t="+tok, nil)); ok {
		t.Fatal("POST 不应接受 ?t=")
	}
	r = httptest.NewRequest("GET", "/api/file", nil)
	if _, ok := svc.DeviceFromRequest(r); ok {
		t.Fatal("无 token 应失败")
	}
	if _, err := svc.Pair("bad", ipA, "dev2", "X"); !errors.Is(err, ErrBadCode) {
		t.Fatalf("错误码: %v", err)
	}
}
