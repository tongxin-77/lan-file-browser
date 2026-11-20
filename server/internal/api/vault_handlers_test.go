package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfb/server/internal/config"
	"lfb/server/internal/organize"
	"lfb/server/internal/thumb"
	"lfb/server/internal/vault"
)

var cheapParams = config.VaultParams{Time: 1, MemoryKiB: 64, Threads: 1, KeyLen: 32}

type vclock struct{ t time.Time }

func (c *vclock) now() time.Time { return c.t }

// newVaultServer 在 newProgressServer 的共享之上加入加密片库（同一目录，带磁盘配置以便管理接口保存）：
//
//	sec    全部设备可见，密码 1234，可下载
//	sec2   仅 dev2 可见，密码 1234
//	other  全部设备可见，密码 5678
func newVaultServer(t *testing.T) (*Server, *vclock) {
	t.Helper()
	old := vault.DefaultParams
	vault.DefaultParams = cheapParams
	t.Cleanup(func() { vault.DefaultParams = old })

	s, root := newProgressServer(t)
	f, err := os.Create(filepath.Join(root, "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	f.Close()
	s.Thumbs = thumb.New(t.TempDir(), "")

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := config.NewStore(cfg)
	hash := func(pw string) *config.Vault {
		v, err := vault.Hash(pw, cheapParams)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	err = st.Update(func(c *config.Config) error {
		c.Shares = append(s.Config.Get().Shares,
			config.Share{ID: "sec", Name: "旅行", Path: root, Perms: config.Perms{Download: true}, Vault: hash("1234")},
			config.Share{ID: "sec2", Name: "日记", Path: root, Devices: []string{"dev2"}, Vault: hash("1234")},
			config.Share{ID: "other", Name: "其他", Path: root, Vault: hash("5678")},
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Config = st
	c := &vclock{time.Unix(2e9, 0)}
	s.vault = vaultState{sessions: vault.NewSessions(c.now), delay: time.Nanosecond}
	return s, c
}

type unlockResp struct {
	Session   *string
	ExpiresIn int
	Shares    []struct{ ID, Name string }
}

func unlockRaw(t *testing.T, s *Server, tok, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/vault/unlock", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func unlock(t *testing.T, s *Server, tok, pw string) unlockResp {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"password": pw})
	w := unlockRaw(t, s, tok, string(b))
	if w.Code != 200 {
		t.Fatalf("unlock: %d %s", w.Code, w.Body)
	}
	return decode[unlockResp](t, w)
}

func mustUnlock(t *testing.T, s *Server, tok, pw string) string {
	t.Helper()
	u := unlock(t, s, tok, pw)
	if u.Session == nil {
		t.Fatalf("密码 %s 应解锁", pw)
	}
	return *u.Session
}

func vreq(t *testing.T, s *Server, method, target, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = local
	r.Host = "127.0.0.1:8686"
	r.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// vaultTargets 为加密片库须隐藏的 GET 接口（%s 处填 shareId）。
var vaultTargets = []string{
	"/api/list?share=%s&path=",
	"/api/file?share=%s&path=a.TXT",
	"/api/download?share=%s&path=a.TXT",
	"/api/thumb?share=%s&path=pic.png",
	"/api/next?share=%s&path=第1集.mkv",
	"/api/subtitles?share=%s&path=第1集.mkv",
}

func target(tpl, share string) string { return strings.Replace(tpl, "%s", share, 1) }

// sameAsMissing 断言访问 share 与访问不存在的共享响应完全相同（状态码、响应体）。
func sameAsMissing(t *testing.T, s *Server, tok, share string, hdr ...string) {
	t.Helper()
	for _, tpl := range vaultTargets {
		got := fget(t, s, tok, target(tpl, share), hdr...)
		want := fget(t, s, tok, target(tpl, "nosuch"), hdr...)
		if got.Code != 404 || got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Errorf("%s: %d %s, 不存在时 %d %s", target(tpl, share), got.Code, got.Body, want.Code, want.Body)
		}
	}
	got := postProgress(t, s, tok, progBody(share, "第1集.mkv", 10, 100))
	want := postProgress(t, s, tok, progBody("nosuch", "第1集.mkv", 10, 100))
	if got.Code != 404 || got.Body.String() != want.Body.String() {
		t.Errorf("progress: %d %s, 不存在时 %s", got.Code, got.Body, want.Body)
	}
}

func shareIDs(t *testing.T, s *Server, tok string, hdr ...string) []string {
	t.Helper()
	w := fget(t, s, tok, "/api/shares", hdr...)
	var body struct{ Items []struct{ ID string } }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var ids []string
	for _, it := range body.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func TestVaultHiddenWhenLocked(t *testing.T) {
	s, _ := newVaultServer(t)
	sameAsMissing(t, s, "tok1", "sec")
	sameAsMissing(t, s, "tok1", "sec", "X-Vault", "bogus")
	if ids := shareIDs(t, s, "tok1"); strings.Contains(strings.Join(ids, ","), "sec") || len(ids) != 3 {
		t.Errorf("/api/shares 不应含加密片库: %v", ids)
	}
	// 普通共享不受影响
	if w := fget(t, s, "tok1", "/api/file?share=pub&path=a.TXT"); w.Code != 200 {
		t.Errorf("普通共享: %d", w.Code)
	}
}

func TestVaultUnlockAccess(t *testing.T) {
	s, _ := newVaultServer(t)
	u := unlock(t, s, "tok1", "1234")
	if u.Session == nil || len(*u.Session) != 43 || u.ExpiresIn != 600 {
		t.Fatalf("解锁: %+v", u)
	}
	if len(u.Shares) != 1 || u.Shares[0].ID != "sec" || u.Shares[0].Name != "旅行" {
		t.Errorf("只应返回对本设备可见且密码匹配的片库: %+v", u.Shares)
	}
	sess := *u.Session
	for _, tpl := range vaultTargets {
		if w := fget(t, s, "tok1", target(tpl, "sec"), "X-Vault", sess); w.Code != 200 {
			t.Errorf("解锁后 %s: %d %s", tpl, w.Code, w.Body)
		}
	}
	// 密码不同的片库仍隐藏；/api/shares 解锁后仍不返回加密片库
	sameAsMissing(t, s, "tok1", "other", "X-Vault", sess)
	if ids := shareIDs(t, s, "tok1", "X-Vault", sess); len(ids) != 3 {
		t.Errorf("/api/shares 解锁后仍不含加密片库: %v", ids)
	}
	// 设备 B 不能用设备 A 的会话
	sameAsMissing(t, s, "tok2", "sec", "X-Vault", sess)

	// 一个密码解锁所有匹配的片库（dev2 可见 sec 与 sec2）
	u2 := unlock(t, s, "tok2", "1234")
	if len(u2.Shares) != 2 {
		t.Errorf("dev2 应解锁 2 个片库: %+v", u2.Shares)
	}
}

func TestVaultUnlockUniformResponse(t *testing.T) {
	s, _ := newVaultServer(t)
	wrong := unlockRaw(t, s, "tok1", `{"password":"0000"}`)
	bad := unlockRaw(t, s, "tok1", `not json`)
	noTok := unlockRaw(t, s, "tok3x", `{"password":"0000"}`) // 无效 token → 401（鉴权在前）
	if noTok.Code != 401 {
		t.Errorf("无 token: %d", noTok.Code)
	}

	plain, _ := newFileServer(t) // 没有任何加密片库
	plain.vault = vaultState{delay: time.Nanosecond}
	none := unlockRaw(t, plain, "tok1", `{"password":"0000"}`)

	for name, w := range map[string]*httptest.ResponseRecorder{"错密码": wrong, "非法请求体": bad, "无加密片库": none} {
		if w.Code != 200 || w.Body.String() != none.Body.String() || w.Header().Get("Content-Type") != none.Header().Get("Content-Type") {
			t.Errorf("%s: %d %q，应与无加密片库一致 %q", name, w.Code, w.Body, none.Body)
		}
	}
	want := `{"expiresIn":600,"session":null,"shares":[]}`
	if got := strings.TrimSpace(none.Body.String()); got != want {
		t.Errorf("空结果 = %s, want %s", got, want)
	}
	// 密码只匹配对本设备不可见的片库：与错密码一致
	s2, _ := newVaultServer(t)
	s2.Config.Update(func(c *config.Config) error {
		c.Share("sec").Devices = []string{"dev2"}
		return nil
	})
	if w := unlockRaw(t, s2, "tok1", `{"password":"1234"}`); w.Body.String() != none.Body.String() {
		t.Errorf("不可见片库: %s", w.Body)
	}
}

func TestVaultUnlockMinDuration(t *testing.T) {
	s, _ := newVaultServer(t)
	s.vault.delay = 150 * time.Millisecond
	for _, pw := range []string{"1234", "0000"} {
		start := time.Now()
		unlock(t, s, "tok1", pw)
		if d := time.Since(start); d < 150*time.Millisecond {
			t.Errorf("密码 %s 耗时 %v，应补齐到最短耗时", pw, d)
		}
	}
}

func TestVaultRateLimit(t *testing.T) {
	s, c := newVaultServer(t)
	for i := 0; i < 5; i++ {
		unlock(t, s, "tok1", "0000")
	}
	if u := unlock(t, s, "tok1", "1234"); u.Session != nil {
		t.Fatal("超限后正确密码也应返回空结果")
	}
	if u := unlock(t, s, "tok2", "1234"); u.Session == nil {
		t.Fatal("限流按设备")
	}
	c.t = c.t.Add(4 * time.Minute)
	if u := unlock(t, s, "tok1", "1234"); u.Session != nil {
		t.Fatal("5 分钟内仍为空结果")
	}
	c.t = c.t.Add(time.Minute + time.Second)
	mustUnlock(t, s, "tok1", "1234")
}

func TestVaultSessionExpiryAndLock(t *testing.T) {
	s, c := newVaultServer(t)
	sess := mustUnlock(t, s, "tok1", "1234")
	c.t = c.t.Add(9 * time.Minute)
	if w := fget(t, s, "tok1", target(vaultTargets[0], "sec"), "X-Vault", sess); w.Code != 200 {
		t.Fatalf("9 分钟内有效: %d", w.Code)
	}
	c.t = c.t.Add(10*time.Minute + time.Second)
	sameAsMissing(t, s, "tok1", "sec", "X-Vault", sess)

	sess = mustUnlock(t, s, "tok1", "1234")
	lock := func(tok, sess string) int {
		r := httptest.NewRequest("POST", "/api/vault/lock", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("X-Vault", sess)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if code := lock("tok2", sess); code != 200 {
		t.Errorf("lock 始终 200: %d", code)
	}
	if w := fget(t, s, "tok1", target(vaultTargets[0], "sec"), "X-Vault", sess); w.Code != 200 {
		t.Fatal("其他设备 lock 不影响本设备会话")
	}
	if code := lock("tok1", sess); code != 200 {
		t.Errorf("lock: %d", code)
	}
	sameAsMissing(t, s, "tok1", "sec", "X-Vault", sess)
	if code := lock("tok1", "nosuch"); code != 200 {
		t.Errorf("无效会话 lock 也 200: %d", code)
	}
}

func TestVaultQueryParamWhitelist(t *testing.T) {
	s, _ := newVaultServer(t)
	sess := mustUnlock(t, s, "tok1", "1234")
	for _, tpl := range vaultTargets {
		w := fget(t, s, "tok1", target(tpl, "sec")+"&v="+sess)
		ok := strings.HasPrefix(tpl, "/api/file") || strings.HasPrefix(tpl, "/api/download") || strings.HasPrefix(tpl, "/api/thumb")
		if ok && w.Code != 200 || !ok && w.Code != 404 {
			t.Errorf("?v= %s: %d（白名单 %v）", tpl, w.Code, ok)
		}
	}
	// 非 GET 不认 ?v=
	r := httptest.NewRequest("POST", "/api/progress?v="+sess, strings.NewReader(progBody("sec", "第1集.mkv", 1, 100)))
	r.Header.Set("Authorization", "Bearer tok1")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("POST ?v=: %d", w.Code)
	}
}

func TestVaultContinue(t *testing.T) {
	s, _ := newVaultServer(t)
	sess := mustUnlock(t, s, "tok1", "1234")
	r := httptest.NewRequest("POST", "/api/progress", strings.NewReader(progBody("sec", "Z目录/x.mp4", 10, 100)))
	r.Header.Set("Authorization", "Bearer tok1")
	r.Header.Set("X-Vault", sess)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("解锁后上报进度: %d %s", w.Code, w.Body)
	}
	shares := func(hdr ...string) string {
		w := fget(t, s, "tok1", "/api/continue", hdr...)
		var body struct{ Items []struct{ Share string } }
		json.Unmarshal(w.Body.Bytes(), &body)
		var ids []string
		for _, it := range body.Items {
			ids = append(ids, it.Share)
		}
		return strings.Join(ids, ",")
	}
	if got := shares(); strings.Contains(got, "sec") {
		t.Errorf("未解锁 continue 应过滤加密片库: %s", got)
	}
	if got := shares("X-Vault", sess); got != "sec" {
		t.Errorf("解锁后 continue 应合并返回: %q", got)
	}
	if got := fget(t, s, "tok2", "/api/continue", "X-Vault", sess).Body.String(); strings.Contains(got, `"sec"`) {
		t.Errorf("他人会话不应返回: %s", got)
	}
}

func TestAdminVault(t *testing.T) {
	s, _ := newVaultServer(t)
	admin := func(method, id, body string) *httptest.ResponseRecorder {
		return vreq(t, s, method, "/api/admin/shares/"+id+"/vault", body, "X-LFB-Admin", "1")
	}
	for _, pw := range []string{"123", "12345678901234567", ""} {
		b, _ := json.Marshal(map[string]string{"password": pw})
		wantErr(t, admin("PUT", "pub", string(b)), 400, CodeBadPath, "密码 "+pw)
	}
	wantErr(t, admin("PUT", "pub", "x"), 400, CodeBadPath, "非法请求体")
	wantErr(t, admin("PUT", "nosuch", `{"password":"1234"}`), 404, CodeNotFound, "不存在")
	wantErr(t, admin("DELETE", "nosuch", ``), 404, CodeNotFound, "不存在")
	if w := vreq(t, s, "PUT", "/api/admin/shares/pub/vault", `{"password":"1234"}`); w.Code != 403 {
		t.Errorf("缺 CSRF 头: %d", w.Code)
	}

	// 设为加密片库（16 位纯数字 / 中文均可）
	if w := admin("PUT", "pub", `{"password":"1234567890123456"}`); w.Code != 200 || !decode[adminShare](t, w).Vault {
		t.Fatalf("设密码: %d %s", w.Code, w.Body)
	}
	v := s.Config.Get().Share("pub").Vault
	if v == nil || !v.Enabled || v.Params != cheapParams || v.Salt == "" || !vault.Verify(v, "1234567890123456") {
		t.Fatalf("配置: %+v", v)
	}
	sameAsMissing(t, s, "tok1", "pub")
	w := vreq(t, s, "GET", "/api/admin/shares", "")
	var items []adminShare
	json.Unmarshal(w.Body.Bytes(), &items)
	n := 0
	for _, it := range items {
		if it.Vault {
			n++
		}
	}
	if len(items) != 6 || n != 4 {
		t.Errorf("管理页应显示全部片库并标记加密: %s", w.Body)
	}

	// 改密码后旧会话失效
	sess := mustUnlock(t, s, "tok1", "1234")
	if w := admin("PUT", "sec", `{"password":"4321"}`); w.Code != 200 {
		t.Fatalf("改密码: %d %s", w.Code, w.Body)
	}
	sameAsMissing(t, s, "tok1", "sec", "X-Vault", sess)
	sess = mustUnlock(t, s, "tok1", "4321")
	if w := fget(t, s, "tok1", target(vaultTargets[0], "sec"), "X-Vault", sess); w.Code != 200 {
		t.Errorf("新密码: %d", w.Code)
	}

	// 取消加密后直接可见；再设回同一密码，旧会话也不复活
	if w := admin("DELETE", "sec", ``); w.Code != 200 || decode[adminShare](t, w).Vault {
		t.Fatalf("取消加密: %d %s", w.Code, w.Body)
	}
	if s.Config.Get().Share("sec").Vault != nil {
		t.Error("取消加密应删除 vault 字段")
	}
	if w := fget(t, s, "tok1", target(vaultTargets[0], "sec")); w.Code != 200 {
		t.Errorf("取消加密后直接可见: %d", w.Code)
	}
	if !strings.Contains(strings.Join(shareIDs(t, s, "tok1"), ","), "sec") {
		t.Error("取消加密后出现在 /api/shares")
	}
	admin("PUT", "sec", `{"password":"4321"}`)
	sameAsMissing(t, s, "tok1", "sec", "X-Vault", sess)

	// 修改共享的其他设置不影响加密
	w = vreq(t, s, "PUT", "/api/admin/shares/sec", `{"name":"旅行2","path":`+jsonStr(s.Config.Get().Share("sec").Path)+`,"perms":{},"devices":[]}`, "X-LFB-Admin", "1")
	if w.Code != 200 || s.Config.Get().Share("sec").Vault == nil {
		t.Errorf("改名后应仍加密: %d %s", w.Code, w.Body)
	}
}

func jsonStr(s string) string {
	var b bytes.Buffer
	json.NewEncoder(&b).Encode(s)
	return strings.TrimSpace(b.String())
}

type orgClock struct{ t time.Time }

func (c *orgClock) Now() time.Time                                   { return c.t }
func (c *orgClock) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// 整理库是加密片库时，未解锁设备看不到整理产生的文件（与不存在一致），解锁后可见。
func TestVaultOrganizedHidden(t *testing.T) {
	s, _ := newVaultServer(t)
	srcDir, lib := t.TempDir(), t.TempDir()
	mt := time.Date(2025, 6, 1, 8, 0, 0, 0, time.Local)
	f := filepath.Join(srcDir, "Show.S01E01.mkv")
	if err := os.WriteFile(f, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(f, mt, mt)
	v, err := vault.Hash("1234", cheapParams)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	s.Config.Update(func(c *config.Config) error {
		c.Shares = append(c.Shares, config.Share{ID: "vlib", Name: "整理库", Path: lib, Vault: v,
			Organize: &config.Organize{Enabled: true, RateLimit: &zero,
				Sources: []config.OrganizeSource{{ID: "s1", Path: srcDir, Mode: config.OrganizeCopy}}}})
		return nil
	})
	clk := &orgClock{time.Now()}
	m := organize.New(organize.Options{Config: s.Config, DB: s.Store, Clock: clk,
		SameVolume: func(a, b string) bool { return false }})
	for range 2 { // 首次扫描只观察，第二次复制
		if err := m.RunOnce(context.Background(), "vlib"); err != nil {
			t.Fatal(err)
		}
		clk.t = clk.t.Add(time.Minute)
	}
	const rel = "剧集/Show/第1季/Show.S01E01.mkv"
	if _, err := os.Stat(filepath.Join(lib, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("应已整理到库内: %v", err)
	}

	for _, tpl := range []string{"/api/list?share=%s&path=", "/api/list?share=%s&path=剧集/Show/第1季", "/api/file?share=%s&path=" + rel} {
		got := fget(t, s, "tok1", target(tpl, "vlib"))
		want := fget(t, s, "tok1", target(tpl, "nosuch"))
		if got.Code != 404 || got.Body.String() != want.Body.String() {
			t.Errorf("未解锁 %s: %d %s", tpl, got.Code, got.Body)
		}
	}
	sess := mustUnlock(t, s, "tok1", "1234")
	if w := fget(t, s, "tok1", target("/api/file?share=%s&path="+rel, "vlib"), "X-Vault", sess); w.Code != 200 {
		t.Errorf("解锁后应可访问: %d %s", w.Code, w.Body)
	}
}
