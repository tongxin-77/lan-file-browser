package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const local = "127.0.0.1:5000"

// adminReq 发出本机管理请求；hdr 为 nil 时带齐 CSRF 所需请求头。
func (e *authEnv) adminReq(method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = local
	r.Host = "127.0.0.1:8686"
	if hdr == nil {
		hdr = map[string]string{"X-LFB-Admin": "1", "Content-Type": "application/json", "Origin": "http://127.0.0.1:8686"}
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("解析响应: %v: %s", err, w.Body)
	}
	return v
}

func TestAdminCSRF(t *testing.T) {
	e := newAuthEnv(t)
	const target = "/api/admin/pairing/refresh"
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"无自定义头", map[string]string{"Content-Type": "application/json"}, 403},
		{"自定义头值不对", map[string]string{"X-LFB-Admin": "0", "Content-Type": "application/json"}, 403},
		{"text/plain 表单", map[string]string{"X-LFB-Admin": "1", "Content-Type": "text/plain"}, 403},
		{"urlencoded 表单", map[string]string{"X-LFB-Admin": "1", "Content-Type": "application/x-www-form-urlencoded"}, 403},
		{"无 Content-Type", map[string]string{"X-LFB-Admin": "1"}, 403},
		{"错误 Origin", map[string]string{"X-LFB-Admin": "1", "Content-Type": "application/json", "Origin": "http://evil.example"}, 403},
		{"Origin 端口不符", map[string]string{"X-LFB-Admin": "1", "Content-Type": "application/json", "Origin": "http://127.0.0.1:9999"}, 403},
		{"Origin 为 null", map[string]string{"X-LFB-Admin": "1", "Content-Type": "application/json", "Origin": "null"}, 403},
		{"正确（无 Origin）", map[string]string{"X-LFB-Admin": "1", "Content-Type": "application/json"}, 200},
		{"正确（localhost）", map[string]string{"X-LFB-Admin": "1", "Content-Type": "application/json; charset=utf-8", "Origin": "http://localhost:8686"}, 200},
		{"正确（[::1]）", map[string]string{"X-LFB-Admin": "1", "Content-Type": "application/json", "Origin": "http://[::1]:8686"}, 200},
	}
	for _, c := range cases {
		w := e.adminReq("POST", target, "{}", c.hdr)
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, w.Code, c.want, w.Body)
		}
	}
	// 其他修改类本机接口同样受保护
	for _, m := range []struct{ method, target string }{
		{"POST", "/api/admin/shares"}, {"PUT", "/api/admin/shares/x"},
		{"DELETE", "/api/admin/shares/x"}, {"DELETE", "/api/devices/x"},
	} {
		if w := e.adminReq(m.method, m.target, "{}", map[string]string{"Content-Type": "text/plain"}); w.Code != 403 {
			t.Errorf("%s %s 无自定义头: %d", m.method, m.target, w.Code)
		}
	}
	// 读取类不要求自定义头，但仍仅限本机
	if w := e.adminReq("GET", "/api/admin/status", "", map[string]string{}); w.Code != 200 {
		t.Errorf("GET status: %d", w.Code)
	}
	if w := e.req("GET", "/api/admin/status", phone, "", nil); w.Code != 403 {
		t.Errorf("局域网访问 status: %d", w.Code)
	}
	if w := e.req("POST", target, phone, "", map[string]string{}); w.Code != 403 {
		t.Errorf("局域网访问 refresh: %d", w.Code)
	}
}

func TestAdminStatus(t *testing.T) {
	e := newAuthEnv(t)
	w := e.adminReq("GET", "/api/admin/status", "", nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	s := decode[struct {
		ServerID, Name, Version string
		Port                    int
		IPs                     []string
	}](t, w)
	if s.ServerID == "" || s.Name == "" || s.Version != "test" || s.Port != 8686 || s.IPs == nil {
		t.Fatalf("字段缺失: %s", w.Body)
	}
}

type pairingResp struct {
	PIN, ExpiresAt, Code, Link, QRSVG string
}

func TestAdminPairingAndRefresh(t *testing.T) {
	e := newAuthEnv(t)
	p := decode[pairingResp](t, e.adminReq("GET", "/api/admin/pairing", "", nil))
	pin, code := e.svc.Codes.Current()
	if p.PIN != pin || p.Code != code || p.ExpiresAt == "" {
		t.Fatalf("pairing: %+v", p)
	}
	if !strings.HasPrefix(p.Link, "lfb://connect?id=") || !strings.Contains(p.Link, "&code="+code) || !strings.Contains(p.Link, "port=8686") {
		t.Fatalf("link: %s", p.Link)
	}
	if !strings.HasPrefix(p.QRSVG, "<svg") || !strings.Contains(p.QRSVG, "<path") {
		t.Fatalf("qrSvg: %.80s", p.QRSVG)
	}

	// 锁定一个 IP，刷新后解除且旧码作废
	for i := 0; i < 5; i++ {
		e.pair("000000x", "devX")
	}
	if w := e.pair(pin, "devX"); w.Code != 423 {
		t.Fatalf("应已锁定: %d", w.Code)
	}
	w := e.adminReq("POST", "/api/admin/pairing/refresh", "", nil)
	if w.Code != 200 {
		t.Fatalf("refresh: %d %s", w.Code, w.Body)
	}
	p2 := decode[pairingResp](t, w)
	if p2.PIN == "" || p2.Code == p.Code {
		t.Fatalf("刷新后未换码: %+v", p2)
	}
	if w := e.pair(p2.PIN, "devX"); w.Code != 200 {
		t.Fatalf("刷新后应解除锁定并可用新 PIN 配对: %d %s", w.Code, w.Body)
	}
}

type adminShareResp struct {
	ID, Name, Path, Status string
	Perms                  struct{ Download, Upload, Delete, Hidden bool }
	Devices                []string
}

type phoneShare struct {
	ID    string
	Name  string
	Perms struct{ Download, Upload, Modify, ShowHidden bool }
}

func TestAdminSharesCRUD(t *testing.T) {
	e := newAuthEnv(t)
	tok := e.pairOK("dev1")
	dir := t.TempDir()
	file := filepath.Join(dir, "a.mp4")
	os.WriteFile(file, []byte("x"), 0o644)
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	bad := []struct{ name, body string }{
		{"非 JSON", "x"},
		{"名称为空", js(map[string]any{"name": " ", "path": dir})},
		{"路径不存在", js(map[string]any{"name": "a", "path": filepath.Join(dir, "nope")})},
		{"路径是文件", js(map[string]any{"name": "a", "path": file})},
		{"相对路径", js(map[string]any{"name": "a", "path": "rel"})},
	}
	for _, c := range bad {
		if w := e.adminReq("POST", "/api/admin/shares", c.body, nil); w.Code != 400 {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}

	w := e.adminReq("POST", "/api/admin/shares", js(map[string]any{
		"name": "电影", "path": dir, "perms": map[string]bool{"download": true},
	}), nil)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	sh := decode[adminShareResp](t, w)
	if len(sh.ID) != 8 || sh.Name != "电影" || !sh.Perms.Download || sh.Status != "ok" || sh.Devices == nil {
		t.Fatalf("create 响应: %s", w.Body)
	}
	if w := e.adminReq("POST", "/api/admin/shares", js(map[string]any{"name": "电影", "path": dir}), nil); w.Code != 409 {
		t.Fatalf("重名应 409: %d", w.Code)
	}

	phoneShares := func() []phoneShare {
		return decode[struct{ Items []phoneShare }](t, e.req("GET", "/api/shares", phone, tok, nil)).Items
	}
	if ps := phoneShares(); len(ps) != 1 || ps[0].ID != sh.ID || !ps[0].Perms.Download || ps[0].Perms.Upload {
		t.Fatalf("手机侧: %+v", ps)
	}

	// 修改：改名、权限、仅 dev2 可见 → dev1 看不到
	w = e.adminReq("PUT", "/api/admin/shares/"+sh.ID, js(map[string]any{
		"name": "片库", "path": dir, "perms": map[string]bool{"upload": true, "delete": true}, "devices": []string{"dev2"},
	}), nil)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	if ps := phoneShares(); len(ps) != 0 {
		t.Fatalf("dev1 不应再看到: %+v", ps)
	}
	e.adminReq("PUT", "/api/admin/shares/"+sh.ID, js(map[string]any{
		"name": "片库", "path": dir, "perms": map[string]bool{"upload": true, "delete": true},
	}), nil)
	if ps := phoneShares(); len(ps) != 1 || ps[0].Name != "片库" || ps[0].Perms.Download || !ps[0].Perms.Upload || !ps[0].Perms.Modify {
		t.Fatalf("修改后手机侧: %+v", ps)
	}
	if w := e.adminReq("PUT", "/api/admin/shares/nope", js(map[string]any{"name": "x", "path": dir}), nil); w.Code != 404 {
		t.Fatalf("修改不存在: %d", w.Code)
	}
	// 另一个共享不能改成重名（大小写不敏感）
	id2 := decode[adminShareResp](t, e.adminReq("POST", "/api/admin/shares", js(map[string]any{"name": "Other", "path": dir}), nil)).ID
	if w := e.adminReq("PUT", "/api/admin/shares/"+id2, js(map[string]any{"name": "OTHER2", "path": dir}), nil); w.Code != 200 {
		t.Fatalf("改名: %d", w.Code)
	}
	if w := e.adminReq("PUT", "/api/admin/shares/"+id2, js(map[string]any{"name": "片库", "path": dir}), nil); w.Code != 409 {
		t.Fatalf("改名重名应 409: %d", w.Code)
	}

	data, _ := os.ReadFile(filepath.Join(e.dir, "config.json"))
	if !strings.Contains(string(data), sh.ID) || !strings.Contains(string(data), "OTHER2") {
		t.Fatalf("config.json 未保存: %s", data)
	}

	list := decode[[]adminShareResp](t, e.adminReq("GET", "/api/admin/shares", "", nil))
	if len(list) != 2 || list[0].ID != sh.ID || list[0].Devices == nil {
		t.Fatalf("list: %+v", list)
	}

	// 删除只移除配置，不动磁盘
	if w := e.adminReq("DELETE", "/api/admin/shares/"+sh.ID, "", nil); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("删除共享不应删除磁盘文件")
	}
	if w := e.adminReq("DELETE", "/api/admin/shares/"+sh.ID, "", nil); w.Code != 404 {
		t.Fatalf("重复删除: %d", w.Code)
	}
	if ps := phoneShares(); len(ps) != 1 || ps[0].ID != id2 {
		t.Fatalf("删除后手机侧: %+v", ps)
	}
}

func TestAdminBrowse(t *testing.T) {
	e := newAuthEnv(t)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "b10"), 0o755)
	os.Mkdir(filepath.Join(dir, "b9"), 0o755)
	os.Mkdir(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, "f.txt"), nil, 0o644)

	type resp struct {
		Path, Parent string
		Items        []struct{ Name, Path string }
	}
	w := e.adminReq("GET", "/api/admin/browse?path="+url.QueryEscape(dir), "", nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r := decode[resp](t, w)
	if len(r.Items) != 2 || r.Items[0].Name != "b9" || r.Items[1].Path != filepath.Join(dir, "b10") || r.Parent != filepath.Dir(dir) {
		t.Fatalf("browse: %s", w.Body)
	}

	r = decode[resp](t, e.adminReq("GET", "/api/admin/browse", "", nil))
	found := false
	for _, it := range r.Items {
		found = found || strings.EqualFold(it.Path, filepath.VolumeName(dir)+`\`)
	}
	if !found {
		t.Fatalf("盘符列表缺少 %s: %+v", filepath.VolumeName(dir), r.Items)
	}

	if w := e.adminReq("GET", "/api/admin/browse?path=rel", "", nil); w.Code != 400 {
		t.Fatalf("相对路径: %d", w.Code)
	}
	if w := e.adminReq("GET", "/api/admin/browse?path="+url.QueryEscape(filepath.Join(dir, "nope")), "", nil); w.Code != 404 {
		t.Fatalf("不存在: %d", w.Code)
	}
	if w := e.req("GET", "/api/admin/browse", phone, "", nil); w.Code != 403 {
		t.Fatalf("局域网访问: %d", w.Code)
	}
}

func TestAdminPage(t *testing.T) {
	e := newAuthEnv(t)
	w := e.adminReq("GET", "/", "", map[string]string{})
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "家庭放映室") {
		t.Fatalf("GET /: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("管理页应禁止被嵌入（防点击劫持）")
	}
	if w := e.adminReq("GET", "/app.js", "", map[string]string{}); w.Code != 200 {
		t.Fatalf("app.js: %d", w.Code)
	}
	for _, js := range []string{"/vault.js", "/organize.js"} {
		w := e.adminReq("GET", js, "", map[string]string{})
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("%s: %d %q", js, w.Code, w.Header().Get("Content-Type"))
		}
		if page := e.adminReq("GET", "/", "", map[string]string{}).Body.String(); !strings.Contains(page, `<script src="`+js+`"></script>`) {
			t.Fatalf("index.html 未引入 %s", js)
		}
		if w := e.req("GET", js, phone, "", nil); w.Code != 403 {
			t.Fatalf("局域网访问 %s: %d", js, w.Code)
		}
	}
	if w := e.req("GET", "/", phone, "", nil); w.Code != 403 {
		t.Fatalf("局域网访问管理页: %d", w.Code)
	}
	if w := e.req("GET", "/app.js", phone, "", nil); w.Code != 403 {
		t.Fatalf("局域网访问静态文件: %d", w.Code)
	}
	if w := e.adminReq("GET", "/nope", "", map[string]string{}); w.Code != 404 {
		t.Fatalf("未知路径: %d", w.Code)
	}
}

// P2 管理接口占位：本机 + CSRF 头齐全时 501；局域网访问 403。
func TestAdminP2Routes(t *testing.T) {
	e := newAuthEnv(t)
	for _, c := range []struct{ method, url string }{
		{"GET", "/api/admin/shares/s1/organize"},
		{"PUT", "/api/admin/shares/s1/organize"},
		{"POST", "/api/admin/shares/s1/organize/scan"},
		{"POST", "/api/admin/shares/s1/organize/dry-run"},
		{"POST", "/api/admin/shares/s1/organize/pause"},
		{"POST", "/api/admin/shares/s1/organize/resume"},
		{"GET", "/api/admin/shares/s1/organize/items"},
		{"POST", "/api/admin/shares/s1/organize/items/retry"},
		{"DELETE", "/api/admin/shares/s1/organize/items/7"},
		{"GET", "/api/admin/organize/defaults"},
		{"PUT", "/api/admin/organize/defaults"},
		{"POST", "/api/admin/organize/check-path"},
	} {
		if w := e.adminReq(c.method, c.url, "{}", nil); w.Code != 501 {
			t.Errorf("%s %s: %d, want 501", c.method, c.url, w.Code)
		}
		if w := e.req(c.method, c.url, phone, "", nil); w.Code != 403 {
			t.Errorf("局域网 %s %s: %d, want 403", c.method, c.url, w.Code)
		}
		if c.method != "GET" {
			if w := e.adminReq(c.method, c.url, "{}", map[string]string{"Content-Type": "application/json"}); w.Code != 403 {
				t.Errorf("缺 X-LFB-Admin %s %s: %d, want 403", c.method, c.url, w.Code)
			}
		}
	}
}
