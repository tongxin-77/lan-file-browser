package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"lfb/server/internal/config"
	"lfb/server/internal/update"
)

// fakeRelease 伪造 GitHub latest release 及其资产（下载地址直接指向伪造服务器）。
func fakeRelease(t *testing.T, tag string) (*httptest.Server, map[string][]byte) {
	assets := map[string][]byte{
		"lfb-server-setup-" + strings.TrimPrefix(tag, "v") + ".exe": []byte("setup"),
		"lfb-app-arm64-v8a.apk":   []byte("0123456789-arm64"),
		"lfb-app-armeabi-v7a.apk": []byte("0123456789-v7a"),
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			var as []string
			for n := range assets {
				as = append(as, fmt.Sprintf(`{"name":%q,"browser_download_url":"%s/dl/%s"}`, n, srv.URL, n))
			}
			as = append(as, fmt.Sprintf(`{"name":"checksums.txt","browser_download_url":"%s/dl/checksums.txt"}`, srv.URL))
			fmt.Fprintf(w, `{"tag_name":%q,"body":"说明","html_url":"%s/page","assets":[%s]}`, tag, srv.URL, strings.Join(as, ","))
			return
		}
		name := filepath.Base(r.URL.Path)
		if name == "checksums.txt" {
			for n, b := range assets {
				h := sha256.Sum256(b)
				fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(h[:]), n)
			}
			return
		}
		w.Write(assets[name])
	}))
	t.Cleanup(srv.Close)
	return srv, assets
}

func newUpdateEnv(t *testing.T, current string) (*authEnv, *update.Checker, map[string][]byte) {
	e := newAuthEnv(t)
	gh, assets := fakeRelease(t, "v1.2.0")
	cfg, err := config.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	st := config.NewStore(cfg)
	c := update.New(update.Options{Current: current, APIBase: gh.URL, CacheDir: filepath.Join(e.dir, "cache", "apk"),
		Settings: func() config.UpdateSettings { return st.Get().Update }})
	t.Cleanup(c.Wait)
	e.h = (&Server{Config: st, Auth: e.svc, Version: current, Updates: c}).Handler()
	return e, c, assets
}

func TestPingMinAppVersion(t *testing.T) {
	e := newAuthEnv(t)
	resp := decode[map[string]any](t, e.req("GET", "/api/ping", phone, "", nil))
	if resp["minAppVersion"] != MinAppVersion || MinAppVersion != "0.0.0" {
		t.Fatalf("ping: %v", resp)
	}
}

func TestAdminUpdate(t *testing.T) {
	e, _, _ := newUpdateEnv(t, "1.1.0")
	if w := e.req("GET", "/api/admin/update", phone, "", nil); w.Code != 403 {
		t.Fatalf("局域网访问: %d", w.Code)
	}
	for _, hdr := range []map[string]string{
		{"Content-Type": "application/json"},
		{"X-LFB-Admin": "1", "Content-Type": "application/json", "Origin": "http://evil.example"},
	} {
		if w := e.adminReq("POST", "/api/admin/update/check", "{}", hdr); w.Code != 403 {
			t.Fatalf("CSRF %v: %d", hdr, w.Code)
		}
	}
	if w := e.req("POST", "/api/admin/update/check", phone, "", nil); w.Code != 403 {
		t.Fatalf("局域网 check: %d", w.Code)
	}

	w := e.adminReq("POST", "/api/admin/update/check", "{}", nil)
	if w.Code != 200 {
		t.Fatalf("check: %d %s", w.Code, w.Body)
	}
	s := decode[update.Status](t, w)
	if !s.HasUpdate || s.Current != "1.1.0" || s.Latest != "1.2.0" || s.Notes != "说明" ||
		!strings.HasSuffix(s.InstallerURL, "/dl/lfb-server-setup-1.2.0.exe") || s.CheckedAt == nil {
		t.Fatalf("check 响应: %s", w.Body)
	}
	s = decode[update.Status](t, e.adminReq("GET", "/api/admin/update", "", map[string]string{}))
	if !s.HasUpdate || s.Latest != "1.2.0" {
		t.Fatalf("GET: %+v", s)
	}
}

func TestAdminUpdateDev(t *testing.T) {
	e, _, _ := newUpdateEnv(t, "dev")
	s := decode[update.Status](t, e.adminReq("POST", "/api/admin/update/check", "{}", nil))
	if s.Disabled != "dev" || s.HasUpdate || s.Latest != "" {
		t.Fatalf("dev: %+v", s)
	}
	tok := e.pairOK("dev1")
	if w := e.req("GET", "/api/app/latest", phone, tok, nil); w.Code != 404 {
		t.Fatalf("无版本信息时: %d %s", w.Code, w.Body)
	}
}

func TestAppLatestAndAPK(t *testing.T) {
	e, c, assets := newUpdateEnv(t, "1.2.0") // 服务端已是最新，App 仍可取到 APK
	tok := e.pairOK("dev1")

	if w := e.req("GET", "/api/app/latest", phone, "", nil); w.Code != 401 {
		t.Fatalf("无 token: %d", w.Code)
	}
	if w := e.req("GET", "/api/app/latest?t="+tok, phone, "", nil); w.Code != 401 {
		t.Fatalf("latest 不应接受 ?t=: %d", w.Code)
	}
	c.Check(context.Background()) // 之后 1 小时内请求不再触发后台检查
	if w := e.req("GET", "/api/app/apk/arm64-v8a", phone, tok, nil); w.Code != 404 {
		t.Fatalf("未缓存时: %d", w.Code)
	}
	if w := e.req("GET", "/api/app/latest", phone, tok, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"apks":{}`) {
		t.Fatalf("未缓存时 latest: %d %s", w.Code, w.Body)
	}
	c.CacheAPKs(context.Background())

	w := e.req("GET", "/api/app/latest", phone, tok, nil)
	if w.Code != 200 {
		t.Fatalf("latest: %d %s", w.Code, w.Body)
	}
	resp := decode[struct {
		Version, MinAppVersion, Notes string
		APKs                          map[string]string `json:"apks"`
		SHA256                        map[string]string `json:"sha256"`
	}](t, w)
	h := sha256.Sum256(assets["lfb-app-arm64-v8a.apk"])
	if resp.Version != "1.2.0" || resp.MinAppVersion != MinAppVersion || resp.Notes != "说明" ||
		resp.APKs["arm64-v8a"] != "http://127.0.0.1:8686/api/app/apk/arm64-v8a" ||
		resp.APKs["armeabi-v7a"] != "http://127.0.0.1:8686/api/app/apk/armeabi-v7a" ||
		resp.SHA256["arm64-v8a"] != hex.EncodeToString(h[:]) || len(resp.SHA256) != 2 {
		t.Fatalf("latest 响应: %s", w.Body)
	}

	// ?t= 直链 + Range
	r := httptest.NewRequest("GET", "/api/app/apk/arm64-v8a?t="+tok, nil)
	r.RemoteAddr = phone
	r.Header.Set("Range", "bytes=2-5")
	rw := httptest.NewRecorder()
	e.h.ServeHTTP(rw, r)
	if rw.Code != 206 || rw.Body.String() != "2345" || rw.Header().Get("Content-Type") != "application/vnd.android.package-archive" {
		t.Fatalf("Range: %d %q %v", rw.Code, rw.Body, rw.Header())
	}
	if w := e.req("GET", "/api/app/apk/armeabi-v7a", phone, tok, nil); w.Code != 200 || w.Body.String() != "0123456789-v7a" {
		t.Fatalf("完整下载: %d %q", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/app/apk/x86", phone, tok, nil); w.Code != 404 {
		t.Fatalf("未知 abi: %d", w.Code)
	}
	if w := e.req("GET", "/api/app/apk/arm64-v8a", phone, "", nil); w.Code != 401 {
		t.Fatalf("无 token: %d", w.Code)
	}
	// ?v=（加密片库会话）与 APK 无关：不代替 ?t=，附带任意值也不影响下载
	if w := e.req("GET", "/api/app/apk/arm64-v8a?v=bogus", phone, "", nil); w.Code != 401 {
		t.Fatalf("仅 ?v=: %d", w.Code)
	}
	if w := e.req("GET", "/api/app/apk/arm64-v8a?t="+tok+"&v=bogus", phone, "", nil); w.Code != 200 || w.Body.String() != "0123456789-arm64" {
		t.Fatalf("?t= 加 ?v=: %d %q", w.Code, w.Body)
	}
	if v := vaultToken(httptest.NewRequest("GET", "/api/app/apk/arm64-v8a?v=x", nil)); v != "" {
		t.Fatalf("/api/app/apk 不应读取 ?v=: %q", v)
	}
}
