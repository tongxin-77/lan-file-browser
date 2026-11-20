package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lfb/server/internal/config"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.0", "v1.2.0", 0},
		{"1.2.0", "1.10.0", -1},
		{"2.0.0", "1.99.99", 1},
		{"1.2.0-beta.1", "1.2.0", -1},
		{"1.2.0-beta.2", "1.2.0-beta.10", -1},
		{"1.2.0-alpha", "1.2.0-alpha.1", -1},
		{"1.2.0-1", "1.2.0-beta", -1},
		{"1.2.0+build.5", "1.2.0", 0},
	}
	for _, c := range cases {
		got, err := Compare(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("Compare(%q,%q)=%d,%v want %d", c.a, c.b, got, err, c.want)
		}
	}
	for _, bad := range []string{"dev", "", "1.2", "1.2.x", "1.2.3-", "1.2.3-a..b", "../1.2.3", "1.2.3/x"} {
		if _, err := Compare(bad, "1.0.0"); err == nil {
			t.Errorf("%q 应无法解析", bad)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	h1 := strings.Repeat("a", 64)
	h2 := strings.Repeat("B", 64)
	m := parseChecksums("" +
		h1 + "  lfb-app-arm64-v8a.apk\n" +
		h2 + " *lfb-server-setup-1.2.0.exe\r\n" +
		"garbage line\n" +
		"1234  short.txt\n")
	if m["lfb-app-arm64-v8a.apk"] != h1 || m["lfb-server-setup-1.2.0.exe"] != strings.ToLower(h2) || len(m) != 2 {
		t.Fatalf("解析结果: %v", m)
	}
}

// fakeGitHub 伪造 GitHub API 与资产下载。资产地址以 https://github.com/ 开头（测试镜像替换），
// 未配镜像时 Checker 的 client 把 github.com 请求转到本服务器。
type fakeGitHub struct {
	t        *testing.T
	srv      *httptest.Server
	tag      string
	status   int               // API 状态码，0 = 200
	assets   map[string][]byte // 资产名 → 内容
	sums     map[string]string // 覆盖 checksums.txt 中的值
	noSums   bool
	apiHits  atomic.Int32
	dlHits   atomic.Int32
	lastPath atomic.Value
	ua       atomic.Value
}

func newFake(t *testing.T, tag string) *fakeGitHub {
	f := &fakeGitHub{t: t, tag: tag, assets: map[string][]byte{
		"lfb-server-setup-" + strings.TrimPrefix(tag, "v") + ".exe": []byte("setup"),
		"lfb-app-arm64-v8a.apk":   []byte("apk-arm64-" + tag),
		"lfb-app-armeabi-v7a.apk": []byte("apk-v7a-" + tag),
	}, sums: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.ua.Store(r.Header.Get("User-Agent"))
	if r.URL.Path == "/repos/tongxin-77/lan-file-browser/releases/latest" {
		f.apiHits.Add(1)
		if f.status != 0 {
			w.WriteHeader(f.status)
			fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
			return
		}
		var as []string
		names := []string{}
		for n := range f.assets {
			names = append(names, n)
		}
		if !f.noSums {
			names = append(names, "checksums.txt")
		}
		for _, n := range names {
			as = append(as, fmt.Sprintf(`{"name":%q,"browser_download_url":"https://github.com/tongxin-77/lan-file-browser/releases/download/%s/%s"}`, n, f.tag, n))
		}
		fmt.Fprintf(w, `{"tag_name":%q,"body":"## 更新\n- 修复","html_url":"https://github.com/tongxin-77/lan-file-browser/releases/tag/%s","assets":[%s]}`,
			f.tag, f.tag, strings.Join(as, ","))
		return
	}
	f.dlHits.Add(1)
	f.lastPath.Store(r.URL.Path)
	name := filepath.Base(r.URL.Path)
	if name == "checksums.txt" {
		for n, b := range f.assets {
			h := sha256.Sum256(b)
			s := hex.EncodeToString(h[:])
			if o, ok := f.sums[n]; ok {
				s = o
			}
			fmt.Fprintf(w, "%s  %s\n", s, n)
		}
		return
	}
	b, ok := f.assets[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(b)
}

// redirect 把发往 github.com 的请求改发到伪造服务器。
type redirect struct{ to string }

func (rt redirect) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "github.com" {
		u := *r.URL
		u.Scheme, u.Host = "http", strings.TrimPrefix(rt.to, "http://")
		r = r.Clone(r.Context())
		r.URL = &u
		r.Host = u.Host
	}
	return http.DefaultTransport.RoundTrip(r)
}

type env struct {
	f   *fakeGitHub
	c   *Checker
	set config.UpdateSettings
	now time.Time
	dir string
}

func newEnv(t *testing.T, current, tag string) *env {
	e := &env{f: newFake(t, tag), set: config.UpdateSettings{Enabled: true}, now: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		dir: filepath.Join(t.TempDir(), "cache", "apk")}
	e.c = New(Options{
		Current:  current,
		APIBase:  e.f.srv.URL,
		CacheDir: e.dir,
		Settings: func() config.UpdateSettings { return e.set },
		Client:   &http.Client{Transport: redirect{e.f.srv.URL}},
		Now:      func() time.Time { return e.now },
	})
	return e
}

func TestCheckNewVersion(t *testing.T) {
	e := newEnv(t, "1.1.0", "v1.2.0")
	if err := e.c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := e.c.Status()
	if !s.HasUpdate || s.Current != "1.1.0" || s.Latest != "1.2.0" || !strings.Contains(s.Notes, "修复") || s.Failed {
		t.Fatalf("状态: %+v", s)
	}
	if s.InstallerURL != "https://github.com/tongxin-77/lan-file-browser/releases/download/v1.2.0/lfb-server-setup-1.2.0.exe" {
		t.Fatalf("安装包地址: %s", s.InstallerURL)
	}
	if s.ReleaseURL == "" || s.CheckedAt == nil {
		t.Fatalf("缺字段: %+v", s)
	}
	if ua, _ := e.f.ua.Load().(string); ua != "lfb-server" {
		t.Fatalf("User-Agent: %q", ua)
	}
}

func TestCheckNoUpdate(t *testing.T) {
	for _, cur := range []string{"1.2.0", "1.3.0"} {
		e := newEnv(t, cur, "v1.2.0")
		if err := e.c.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		if s := e.c.Status(); s.HasUpdate || s.Latest != "1.2.0" {
			t.Fatalf("%s: %+v", cur, s)
		}
	}
}

func TestDevAndDisabledSkip(t *testing.T) {
	e := newEnv(t, "dev", "v1.2.0")
	e.c.Check(context.Background())
	e.c.MaybeRefresh()
	if e.f.apiHits.Load() != 0 {
		t.Fatal("dev 版本不应检查")
	}
	if s := e.c.Status(); s.Disabled != "dev" || s.HasUpdate {
		t.Fatalf("dev 状态: %+v", s)
	}

	e = newEnv(t, "1.0.0", "v1.2.0")
	e.set.Enabled = false
	e.c.Check(context.Background())
	if e.f.apiHits.Load() != 0 {
		t.Fatal("关闭后不应检查")
	}
	if s := e.c.Status(); s.Disabled != "config" {
		t.Fatalf("关闭状态: %+v", s)
	}
}

// 限流 / 网络失败：返回错误但保留上次结果，状态标记 failed，不 panic。
func TestCheckFailureSilent(t *testing.T) {
	e := newEnv(t, "1.0.0", "v1.2.0")
	if err := e.c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.f.status = http.StatusForbidden
	if err := e.c.Check(context.Background()); err == nil {
		t.Fatal("限流应返回错误")
	}
	s := e.c.Status()
	if !s.Failed || s.Latest != "1.2.0" || !s.HasUpdate {
		t.Fatalf("失败后应保留上次结果: %+v", s)
	}

	e = newEnv(t, "1.0.0", "v1.2.0")
	e.c.opt.APIBase = "http://127.0.0.1:1" // 连不上
	if err := e.c.Check(context.Background()); err == nil {
		t.Fatal("连接失败应返回错误")
	}
	if s := e.c.Status(); !s.Failed || s.Latest != "" {
		t.Fatalf("状态: %+v", s)
	}
}

// 结果缓存 1 小时：期间 MaybeRefresh 不发请求；过期后重新检查。
func TestMaybeRefreshCache(t *testing.T) {
	e := newEnv(t, "1.0.0", "v1.2.0")
	e.c.Check(context.Background())
	e.c.MaybeRefresh()
	e.c.Wait()
	if n := e.f.apiHits.Load(); n != 1 {
		t.Fatalf("1 小时内不应重复检查: %d", n)
	}
	e.now = e.now.Add(61 * time.Minute)
	e.c.MaybeRefresh()
	e.c.Wait()
	if n := e.f.apiHits.Load(); n != 2 {
		t.Fatalf("过期后应重新检查: %d", n)
	}
}

func TestCacheAPKs(t *testing.T) {
	e := newEnv(t, "1.0.0", "v1.2.0")
	e.c.Check(context.Background())
	e.c.CacheAPKs(context.Background())
	for _, abi := range ABIs {
		p, ver, ok := e.c.APK(abi)
		if !ok || ver != "1.2.0" || filepath.Dir(p) != filepath.Join(e.dir, "1.2.0") {
			t.Fatalf("%s: %s %s %v", abi, p, ver, ok)
		}
		if b, _ := os.ReadFile(p); string(b) != "apk-"+map[string]string{"arm64-v8a": "arm64", "armeabi-v7a": "v7a"}[abi]+"-v1.2.0" {
			t.Fatalf("%s 内容: %q", abi, b)
		}
	}
	rel, apks := e.c.App()
	if rel == nil || len(apks) != 2 || len(rel.SHA256) != 3 {
		t.Fatalf("App(): %+v %v", rel, apks)
	}

	// 已缓存且校验一致时不重复下载
	n := e.f.dlHits.Load()
	e.c.CacheAPKs(context.Background())
	if e.f.dlHits.Load() != n {
		t.Fatal("已缓存不应重新下载")
	}

	// 新版本：只保留最新一个版本的缓存
	e.f.tag = "v1.3.0"
	e.f.assets["lfb-app-arm64-v8a.apk"] = []byte("apk-arm64-v1.3.0")
	e.c.Check(context.Background())
	e.c.CacheAPKs(context.Background())
	if _, err := os.Stat(filepath.Join(e.dir, "1.2.0")); !os.IsNotExist(err) {
		t.Fatalf("旧版本缓存应删除: %v", err)
	}
	if _, ver, ok := e.c.APK("arm64-v8a"); !ok || ver != "1.3.0" {
		t.Fatalf("新版本: %s %v", ver, ok)
	}
	if _, _, ok := e.c.APK("x86"); ok {
		t.Fatal("未知 abi")
	}
}

func TestCacheAPKChecksumMismatch(t *testing.T) {
	e := newEnv(t, "1.0.0", "v1.2.0")
	e.f.sums["lfb-app-armeabi-v7a.apk"] = strings.Repeat("0", 64)
	e.c.Check(context.Background())
	e.c.CacheAPKs(context.Background())
	if _, _, ok := e.c.APK("arm64-v8a"); !ok {
		t.Fatal("校验通过的应缓存")
	}
	if _, _, ok := e.c.APK("armeabi-v7a"); ok {
		t.Fatal("校验失败不应缓存")
	}
	ents, _ := os.ReadDir(filepath.Join(e.dir, "1.2.0"))
	for _, en := range ents {
		if strings.Contains(en.Name(), "armeabi") {
			t.Fatalf("校验失败的文件应删除: %s", en.Name())
		}
	}
}

func TestCacheAPKNoChecksums(t *testing.T) {
	e := newEnv(t, "1.0.0", "v1.2.0")
	e.f.noSums = true
	e.c.Check(context.Background())
	e.c.CacheAPKs(context.Background())
	if _, _, ok := e.c.APK("arm64-v8a"); ok {
		t.Fatal("无 checksums 不应缓存")
	}
}

func TestMirror(t *testing.T) {
	e := newEnv(t, "1.0.0", "v1.2.0")
	e.set.Mirror = e.f.srv.URL + "/mirror" // 不带结尾斜杠也可
	e.c.opt.Client = http.DefaultClient    // 不再改写 github.com：下载必须走镜像
	if err := e.c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := e.f.srv.URL + "/mirror/tongxin-77/lan-file-browser/releases/download/v1.2.0/lfb-server-setup-1.2.0.exe"
	if s := e.c.Status(); s.InstallerURL != want {
		t.Fatalf("安装包地址 %s, want %s", s.InstallerURL, want)
	}
	e.c.CacheAPKs(context.Background())
	if _, _, ok := e.c.APK("arm64-v8a"); !ok {
		t.Fatal("经镜像下载失败")
	}
	if p, _ := e.f.lastPath.Load().(string); !strings.HasPrefix(p, "/mirror/tongxin-77/") {
		t.Fatalf("下载路径: %s", p)
	}
}
