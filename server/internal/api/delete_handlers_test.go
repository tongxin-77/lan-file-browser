package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lfb/server/internal/config"
	"lfb/server/internal/organize"
)

// fakeRecycler 记录调用并直接删除；文件名命中 fail 时返回对应错误。
type fakeRecycler struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error // 文件名 → 错误
}

func (r *fakeRecycler) Recycle(p string) error {
	r.mu.Lock()
	r.calls = append(r.calls, p)
	err := r.fail[filepath.Base(p)]
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return os.RemoveAll(p)
}

// newDeleteServer 在 newVaultServer 之上加：
//
//	del   全部设备可见，允许删除，不显示隐藏文件
//	vdel  加密片库（密码 1234），允许删除
//
// 并注入假回收站。
func newDeleteServer(t *testing.T) (*Server, string, *fakeRecycler) {
	t.Helper()
	s, _ := newVaultServer(t)
	root := s.Config.Get().Shares[0].Path
	v := s.Config.Get().Shares[3].Vault // sec 的密码 1234
	err := s.Config.Update(func(c *config.Config) error {
		c.Shares = append(c.Shares,
			config.Share{ID: "del", Name: "可删", Path: root, Perms: config.Perms{Delete: true}},
			config.Share{ID: "vdel", Name: "片库可删", Path: root, Perms: config.Perms{Delete: true}, Vault: v},
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := &fakeRecycler{fail: map[string]error{}}
	s.Recycler = rec
	return s, root, rec
}

func postDelete(t *testing.T, s *Server, tok, share string, paths []string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"share": share, "paths": paths})
	r := httptest.NewRequest("POST", "/api/delete", strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// failedOf 断言 200 + failed 列表，返回 path → code。
func failedOf(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body struct {
		Failed []struct{ Path, Code, Message string }
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Failed) == 0 {
		t.Fatalf("应为 200 + failed: %d %s", w.Code, w.Body)
	}
	m := map[string]string{}
	for _, f := range body.Failed {
		if f.Message == "" {
			t.Errorf("%s 缺少 message", f.Path)
		}
		m[f.Path] = f.Code
	}
	return m
}

func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func TestDeleteOK(t *testing.T) {
	s, root, rec := newDeleteServer(t)
	etag := stableETag(t, s, q("/api/list", "del", ""))
	w := postDelete(t, s, "tok1", "del", []string{"a.TXT", "Z目录", "中文 名.mp4"})
	if w.Code != 204 {
		t.Fatalf("全部成功应 204: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"a.TXT", "Z目录", "中文 名.mp4"} {
		if exists(root, p) {
			t.Errorf("%s 应已删除", p)
		}
	}
	for _, c := range rec.calls {
		if !filepath.IsAbs(c) {
			t.Errorf("回收站应收到绝对路径: %s", c)
		}
	}
	if stableETag(t, s, q("/api/list", "del", "")) == etag {
		t.Error("删除后列表 ETag 应变化")
	}
}

func TestDeleteRequestErrors(t *testing.T) {
	s, root, _ := newDeleteServer(t)
	wantErr(t, postDelete(t, s, "bad", "del", []string{"a.TXT"}), 401, CodeUnauthorized, "无 token")
	wantErr(t, postDelete(t, s, "tok1", "pub", []string{"a.TXT"}), 403, CodeForbidden, "无删除权限")
	wantErr(t, postDelete(t, s, "tok2", "priv", []string{"a.TXT"}), 404, CodeNotFound, "不可见共享")
	wantErr(t, postDelete(t, s, "tok1", "nosuch", []string{"a.TXT"}), 404, CodeNotFound, "不存在的共享")
	wantErr(t, postDelete(t, s, "tok1", "del", nil), 400, CodeBadPath, "空 paths")
	if !exists(root, "a.TXT") {
		t.Fatal("请求被拒时不应删除任何文件")
	}

	// 非 JSON 请求体
	r := httptest.NewRequest("POST", "/api/delete", strings.NewReader(`share=del&paths=a.TXT`))
	r.Header.Set("Authorization", "Bearer tok1")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	wantErr(t, w, 400, CodeBadPath, "非 JSON")

	// 离线共享
	s.Config.Update(func(c *config.Config) error {
		c.Shares[2].Perms.Delete = true
		return nil
	})
	wantErr(t, postDelete(t, s, "tok1", "gone", []string{"a.TXT"}), 503, CodeShareOffline, "离线共享")
}

// 共享根、路径逃逸、隐藏/系统项、链接逃逸：逐项失败，不删除；其余照常删除（部分失败格式）。
func TestDeletePerPathRules(t *testing.T) {
	s, root, rec := newDeleteServer(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "o.txt"), []byte("o"), 0o644)
	mklinkJ(t, filepath.Join(root, "outlink"), outside)

	paths := []string{"", "../x", "a/../../x", `C:\Windows`, "/abs", ".dot", "attrhidden.txt", "hdir/in.txt",
		"desktop.ini", "$RECYCLE.BIN", ".lfb-upload-abcd", "outlink", "outlink/o.txt", "nope.txt", "a.TXT"}
	got := failedOf(t, postDelete(t, s, "tok1", "del", paths))
	want := map[string]string{
		"": CodeBadPath, "../x": CodeBadPath, "a/../../x": CodeBadPath, `C:\Windows`: CodeBadPath, "/abs": CodeBadPath,
		".dot": CodeNotFound, "attrhidden.txt": CodeNotFound, "hdir/in.txt": CodeNotFound, "desktop.ini": CodeNotFound,
		"$RECYCLE.BIN": CodeNotFound, ".lfb-upload-abcd": CodeNotFound, "outlink": CodeNotFound, "outlink/o.txt": CodeNotFound,
		"nope.txt": CodeNotFound,
	}
	if len(got) != len(want) {
		t.Errorf("failed = %v", got)
	}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("%q: %q, want %q", p, got[p], c)
		}
	}
	if exists(root, "a.TXT") {
		t.Error("合法路径应照常删除")
	}
	for _, p := range []string{".dot", "attrhidden.txt", "hdir/in.txt", "desktop.ini", "outlink"} {
		if !exists(root, p) {
			t.Errorf("%s 不应被删除", p)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "o.txt")); err != nil {
		t.Error("共享外文件不应被删除")
	}
	if len(rec.calls) != 1 {
		t.Errorf("只应对 a.TXT 调用回收站: %v", rec.calls)
	}
	// 有「显示隐藏文件」权限时隐藏项可删（priv）
	if w := postDelete(t, s, "tok1", "priv", []string{".dot"}); w.Code != 204 {
		t.Errorf("priv 删除隐藏项: %d %s", w.Code, w.Body)
	}
}

// 加密片库：无会话与不存在的共享完全相同；有会话可删。
func TestDeleteVault(t *testing.T) {
	s, root, _ := newDeleteServer(t)
	for _, hdr := range [][]string{nil, {"X-Vault", "bogus"}} {
		got := postDelete(t, s, "tok1", "vdel", []string{"a.TXT"}, hdr...)
		want := postDelete(t, s, "tok1", "nosuch", []string{"a.TXT"}, hdr...)
		if got.Code != 404 || got.Body.String() != want.Body.String() {
			t.Errorf("锁定片库: %d %s, 不存在时 %s", got.Code, got.Body, want.Body)
		}
	}
	if !exists(root, "a.TXT") {
		t.Fatal("锁定时不应删除")
	}
	sess := mustUnlock(t, s, "tok1", "1234")
	if w := postDelete(t, s, "tok1", "vdel", []string{"a.TXT"}, "X-Vault", sess); w.Code != 204 {
		t.Fatalf("解锁后删除: %d %s", w.Code, w.Body)
	}
	if exists(root, "a.TXT") {
		t.Error("解锁后应已删除")
	}
}

// 正在播放（活跃流或 60 秒内有进度上报）的文件及其所在文件夹拒绝删除；回收站报告占用 / 无回收站时给出对应错误码。
func TestDeleteInUse(t *testing.T) {
	s, root, rec := newDeleteServer(t)
	mustPost(t, s, "tok2", "pub", "Z目录/x.mp4", 10, 100) // 另一台设备经另一个共享在播放
	mustPost(t, s, "tok1", "del", "video.mkv", 10, 100)
	end := s.Activity.StreamFile(filepath.Join(root, "第1集.mkv"))
	defer end()
	rec.fail["第2集.mkv"] = organize.ErrInUse
	rec.fail["第10集.mkv"] = organize.ErrNoRecycleBin
	rec.fail["a.TXT"] = errors.New("其他错误")

	got := failedOf(t, postDelete(t, s, "tok1", "del",
		[]string{"Z目录", "Z目录/x.mp4", "video.mkv", "第1集.mkv", "第2集.mkv", "第10集.mkv", "a.TXT", "中文 名.mp4"}))
	want := map[string]string{"Z目录": CodeInUse, "Z目录/x.mp4": CodeInUse, "video.mkv": CodeInUse, "第1集.mkv": CodeInUse,
		"第2集.mkv": CodeInUse, "第10集.mkv": CodeNoRecycleBin, "a.TXT": CodeInternal}
	if len(got) != len(want) {
		t.Errorf("failed = %v", got)
	}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("%s: %q, want %q", p, got[p], c)
		}
	}
	for _, p := range []string{"Z目录/x.mp4", "video.mkv", "第1集.mkv"} {
		if !exists(root, p) {
			t.Errorf("%s 不应被删除", p)
		}
	}
	if exists(root, "中文 名.mp4") {
		t.Error("未被占用的文件应删除")
	}
}
