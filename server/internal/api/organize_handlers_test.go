package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lfb/server/internal/config"
	"lfb/server/internal/organize"
	"lfb/server/internal/store"
)

type orgEnv struct {
	t    *testing.T
	base string
	s    *Server
	lib  string
}

func newOrgEnv(t *testing.T) *orgEnv {
	t.Helper()
	base := t.TempDir()
	c, err := config.Load(filepath.Join(base, "data"))
	if err != nil {
		t.Fatal(err)
	}
	e := &orgEnv{t: t, base: base, lib: e2dir(t, base, "lib")}
	c.Shares = append(c.Shares, config.Share{ID: "sh1", Name: "库", Path: e.lib},
		config.Share{ID: "sh2", Name: "库2", Path: e2dir(t, base, "lib2")})
	db, err := store.Open(filepath.Join(base, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cs := config.NewStore(c)
	e.s = &Server{Config: cs, Auth: TokenMap{}, Version: "test", Store: db,
		Organizer: organize.New(organize.Options{Config: cs, DB: db})}
	return e
}

func e2dir(t *testing.T, base, rel string) string {
	p := filepath.Join(base, rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *orgEnv) do(method, target string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, target, strings.NewReader(string(b)))
	r.RemoteAddr = local
	r.Host = "127.0.0.1:8686"
	r.Header.Set("X-LFB-Admin", "1")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.s.Handler().ServeHTTP(w, r)
	return w
}

type orgResp struct {
	Organize config.Organize         `json:"organize"`
	Defaults config.OrganizeDefaults `json:"defaults"`
	Sources  []map[string]any        `json:"sources"`
	Status   map[string]any          `json:"status"`
}

func (e *orgEnv) put(share string, o map[string]any) *httptest.ResponseRecorder {
	return e.do("PUT", "/api/admin/shares/"+share+"/organize", o)
}

func src(path, mode string) map[string]any { return map[string]any{"path": path, "mode": mode} }

func TestOrganizeSettings(t *testing.T) {
	e := newOrgEnv(t)
	w := e.do("GET", "/api/admin/shares/sh1/organize", nil)
	if w.Code != 200 {
		t.Fatalf("GET: %d %s", w.Code, w.Body)
	}
	if r := decode[orgResp](t, w); r.Organize.Enabled || len(r.Organize.Sources) != 0 || r.Defaults.Interval != 30 {
		t.Fatalf("初始: %+v", r)
	}
	dl := e2dir(t, e.base, "dl")
	w = e.put("sh1", map[string]any{"enabled": true, "rateLimit": 20,
		"sources": []any{src(dl, "copy"), map[string]any{"path": e2dir(t, e.base, "dl2"), "mode": "move"}}})
	if w.Code != 200 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	r := decode[orgResp](t, w)
	if len(r.Organize.Sources) != 2 || r.Organize.Sources[0].ID == "" || r.Organize.Sources[1].AfterMove != "recycle" ||
		*r.Organize.RateLimit != 20 || len(r.Sources) != 2 || r.Sources[0]["sameDisk"] != true || r.Sources[0]["sameVolume"] != true {
		t.Fatalf("保存结果: %+v", r)
	}
	if sh := e.s.Config.Get().Share("sh1"); sh.Organize == nil || !sh.Organize.Enabled {
		t.Fatal("应写入配置")
	}
	// 暂停 / 继续写入 paused
	if w := e.do("POST", "/api/admin/shares/sh1/organize/pause", nil); w.Code != 200 || !e.s.Config.Get().Share("sh1").Organize.Paused {
		t.Fatalf("pause: %d", w.Code)
	}
	if r := decode[orgResp](t, e.do("GET", "/api/admin/shares/sh1/organize", nil)); r.Status["state"] != "paused" || r.Status["pauseReason"] != "manual" {
		t.Fatalf("状态: %+v", r.Status)
	}
	if w := e.do("POST", "/api/admin/shares/sh1/organize/resume", nil); w.Code != 200 || e.s.Config.Get().Share("sh1").Organize.Paused {
		t.Fatalf("resume: %d", w.Code)
	}
	if w := e.do("POST", "/api/admin/shares/sh1/organize/scan", nil); w.Code != 202 {
		t.Fatalf("scan: %d", w.Code)
	}
	if w := e.do("POST", "/api/admin/shares/sh2/organize/scan", nil); w.Code != 409 {
		t.Fatalf("未开启时 scan: %d", w.Code)
	}
	if w := e.do("GET", "/api/admin/shares/nope/organize", nil); w.Code != 404 {
		t.Fatalf("不存在的片库: %d", w.Code)
	}
}

// 路径校验在管理接口中执行（06 文档第 9 节）。
func TestOrganizePutValidation(t *testing.T) {
	e := newOrgEnv(t)
	dl := e2dir(t, e.base, "dl")
	if w := e.put("sh1", map[string]any{"enabled": true, "sources": []any{src(dl, "copy")}}); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	cases := []struct {
		name  string
		share string
		body  map[string]any
		want  int
	}{
		{"相对路径", "sh1", map[string]any{"sources": []any{src(`dl`, "copy")}}, 400},
		{"不存在", "sh1", map[string]any{"sources": []any{src(filepath.Join(e.base, "nope"), "copy")}}, 400},
		{"模式非法", "sh1", map[string]any{"sources": []any{src(dl, "zip")}}, 400},
		{"afterMove 非法", "sh1", map[string]any{"sources": []any{map[string]any{"path": dl, "mode": "move", "afterMove": "x"}}}, 400},
		{"源=库", "sh1", map[string]any{"sources": []any{src(e.lib, "copy")}}, 400},
		{"源在库内", "sh1", map[string]any{"sources": []any{src(e2dir(t, e.base, "lib/in"), "copy")}}, 400},
		{"库在源内", "sh1", map[string]any{"sources": []any{src(e.base, "copy")}}, 400},
		{"源互相包含", "sh1", map[string]any{"sources": []any{src(dl, "copy"), src(e2dir(t, e.base, "dl/sub"), "copy")}}, 400},
		{"与其他片库源重叠", "sh2", map[string]any{"sources": []any{src(e2dir(t, e.base, "dl/x"), "copy")}}, 409},
		{"其他片库的库目录在源内", "sh2", map[string]any{"sources": []any{src(e.lib, "copy")}}, 400},
		{"间隔为 0", "sh1", map[string]any{"interval": 0, "sources": []any{}}, 400},
		{"负限速", "sh1", map[string]any{"rateLimit": -1, "sources": []any{}}, 400},
	}
	for _, c := range cases {
		w := e.put(c.share, c.body)
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, w.Code, c.want, w.Body)
		}
		if strings.Contains(w.Body.String(), strings.ReplaceAll(e.base, `\`, `\\`)) {
			t.Errorf("%s: 错误信息不应含真实路径: %s", c.name, w.Body)
		}
	}
	if sh := e.s.Config.Get().Share("sh1"); len(sh.Organize.Sources) != 1 {
		t.Fatal("校验失败时不应修改配置")
	}
}

func TestOrganizeDryRunAndItems(t *testing.T) {
	e := newOrgEnv(t)
	dl := e2dir(t, e.base, "dl")
	os.WriteFile(filepath.Join(dl, "Show.S01E01.mkv"), []byte("video"), 0o644)
	os.WriteFile(filepath.Join(dl, "a.mkv.part"), []byte("x"), 0o644)
	e.put("sh1", map[string]any{"enabled": true, "sources": []any{src(dl, "copy")}})
	w := e.do("POST", "/api/admin/shares/sh1/organize/dry-run", nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var dr struct {
		Items []organize.DryItem
		Total int
	}
	json.Unmarshal(w.Body.Bytes(), &dr)
	if dr.Total != 1 || dr.Items[0].TargetPath != "剧集/Show/第1季/Show.S01E01.mkv" || dr.Items[0].Action != "copy" {
		t.Fatalf("%+v", dr)
	}
	if entries, _ := os.ReadDir(e.lib); len(entries) != 0 {
		t.Fatal("试运行不应产生文件")
	}
	w = e.do("GET", "/api/admin/shares/sh1/organize/items?state=done", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("items: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{"?state=x", "?limit=0", "?offset=-1"} {
		if w := e.do("GET", "/api/admin/shares/sh1/organize/items"+bad, nil); w.Code != 400 {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	if w := e.do("DELETE", "/api/admin/shares/sh1/organize/items/99", nil); w.Code != 404 {
		t.Errorf("删除不存在记录: %d", w.Code)
	}
	if w := e.do("POST", "/api/admin/shares/sh1/organize/items/retry", map[string]any{"ids": []int{1}}); w.Code != 200 {
		t.Errorf("retry: %d", w.Code)
	}
}

func TestOrganizeDefaultsAndCheckPath(t *testing.T) {
	e := newOrgEnv(t)
	w := e.do("PUT", "/api/admin/organize/defaults", map[string]any{"interval": 10, "rateLimit": 0, "maxRetries": 5, "minFree": 50})
	if w.Code != 200 || e.s.Config.Get().OrganizeDefaults.Interval != 10 || *e.s.Config.Get().OrganizeDefaults.MinFree != 50 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, bad := range []map[string]any{{"interval": 0, "maxRetries": 1}, {"interval": 5, "rateLimit": -1}, {"interval": 5, "afterMove": "x"}} {
		if w := e.do("PUT", "/api/admin/organize/defaults", bad); w.Code != 400 {
			t.Errorf("%v: %d", bad, w.Code)
		}
	}
	if w := e.do("GET", "/api/admin/organize/defaults", nil); !strings.Contains(w.Body.String(), `"interval":10`) {
		t.Fatal(w.Body)
	}
	w = e.do("POST", "/api/admin/organize/check-path", map[string]string{"sharePath": e.lib, "sourcePath": filepath.Join(e.lib, "x")})
	if w.Code != 200 || w.Body.String() != `{"sameDisk":true,"sameVolume":true,"nested":true}`+"\n" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/admin/organize/check-path", map[string]string{"sharePath": "x", "sourcePath": "y"}); w.Code != 400 {
		t.Fatalf("相对路径: %d", w.Code)
	}
}

// 局域网访问一律 403；修改类缺 CSRF 头 403。
func TestOrganizeLocalOnly(t *testing.T) {
	e := newOrgEnv(t)
	h := e.s.Handler()
	for _, c := range []struct{ m, u string }{
		{"GET", "/api/admin/shares/sh1/organize"}, {"PUT", "/api/admin/shares/sh1/organize"},
		{"POST", "/api/admin/shares/sh1/organize/dry-run"}, {"GET", "/api/admin/organize/defaults"},
	} {
		r := httptest.NewRequest(c.m, c.u, strings.NewReader("{}"))
		r.RemoteAddr = phone
		r.Header.Set("X-LFB-Admin", "1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("局域网 %s %s: %d", c.m, c.u, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/admin/shares/sh1/organize/dry-run", strings.NewReader("{}"))
	r.RemoteAddr, r.Host = local, "127.0.0.1:8686"
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("缺 CSRF 头: %d", w.Code)
	}
}
