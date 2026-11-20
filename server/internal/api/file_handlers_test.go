package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"lfb/server/internal/config"
)

// newFileServer 构建临时共享：
//
//	pub   全部设备可见，Hidden 关、Download 关
//	priv  仅 dev1 可见，Hidden 开、Download 开
//	gone  路径不存在（离线）
func newFileServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"第10集.mkv":         "10",
		"第2集.mkv":          "2",
		"第1集.mkv":          "1",
		"a.TXT":            "aaaa",
		"Z目录/x.mp4":        "x",
		"b目录/sub.srt":      "s",
		".dot":             "d",
		"attrhidden.txt":   "h",
		"hdir/in.txt":      "in",
		"desktop.ini":      "ini",
		"$RECYCLE.BIN/r":   "r",
		".lfb-upload-abcd": "u",
		"video.mkv":        "0123456789",
		"中文 名.mp4":         "zh",
	}
	for rel, c := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"attrhidden.txt", "hdir"} {
		p, _ := syscall.UTF16PtrFromString(filepath.Join(root, rel))
		if err := syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Shares: []config.Share{
		{ID: "pub", Name: "公共", Path: root},
		{ID: "priv", Name: "私有", Path: root, Devices: []string{"dev1"}, Perms: config.Perms{Download: true, Hidden: true, Delete: true}},
		{ID: "gone", Name: "移动硬盘", Path: filepath.Join(root, "no-such-dir")},
	}}
	return &Server{Config: config.NewStore(cfg), Auth: TokenMap{"tok1": "dev1", "tok2": "dev2"}}, root
}

func fget(t *testing.T, s *Server, tok, target string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", target, nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func q(endpoint, share, path string, extra ...string) string {
	v := url.Values{"share": {share}, "path": {path}}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return endpoint + "?" + v.Encode()
}

type listResp struct {
	Items []struct {
		Name     string
		Kind     string
		Size     int64
		MTime    int64
		Hidden   bool
		HasThumb bool
		Progress any
	}
	Total int
}

func list(t *testing.T, s *Server, tok, target string) listResp {
	t.Helper()
	w := fget(t, s, tok, target)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", target, w.Code, w.Body)
	}
	var lr listResp
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil {
		t.Fatal(err)
	}
	return lr
}

func names(lr listResp) []string {
	var n []string
	for _, it := range lr.Items {
		n = append(n, it.Name)
	}
	return n
}

func wantErr(t *testing.T, w *httptest.ResponseRecorder, status int, code, what string) {
	t.Helper()
	var body struct{ Error struct{ Code string } }
	json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != status || body.Error.Code != code {
		t.Errorf("%s: %d %s, want %d %s", what, w.Code, w.Body, status, code)
	}
}

func TestShares(t *testing.T) {
	s, _ := newFileServer(t)
	get := func(tok string) map[string]map[string]any {
		w := fget(t, s, tok, "/api/shares")
		var body struct{ Items []map[string]any }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		m := map[string]map[string]any{}
		for _, it := range body.Items {
			m[it["id"].(string)] = it
		}
		return m
	}
	m := get("tok2")
	if _, ok := m["priv"]; ok || len(m) != 2 {
		t.Fatalf("dev2 不应看到 priv: %v", m)
	}
	m = get("tok1")
	if len(m) != 3 {
		t.Fatalf("dev1 应看到 3 个共享: %v", m)
	}
	if m["pub"]["status"] != "ok" || m["gone"]["status"] != "offline" {
		t.Errorf("status: %v %v", m["pub"]["status"], m["gone"]["status"])
	}
	p := m["priv"]["perms"].(map[string]any)
	want := map[string]any{"download": true, "upload": false, "modify": true, "showHidden": true}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("perms.%s = %v, want %v (%v)", k, p[k], v, p)
		}
	}
}

func TestShareVisibilityAndOffline(t *testing.T) {
	s, _ := newFileServer(t)
	for _, ep := range []string{"/api/list", "/api/file", "/api/download"} {
		wantErr(t, fget(t, s, "tok2", q(ep, "priv", "video.mkv")), 404, CodeNotFound, ep+" 不可见共享")
		wantErr(t, fget(t, s, "tok1", q(ep, "nope", "")), 404, CodeNotFound, ep+" 不存在共享")
		wantErr(t, fget(t, s, "tok1", q(ep, "gone", "x.mkv")), 503, CodeShareOffline, ep+" 离线共享")
	}
	lr := list(t, s, "tok1", q("/api/list", "priv", ""))
	if lr.Total == 0 {
		t.Fatal("dev1 应能列出 priv")
	}
}

func TestListFilterAndKinds(t *testing.T) {
	s, _ := newFileServer(t)

	lr := list(t, s, "tok1", q("/api/list", "pub", ""))
	got := names(lr)
	want := []string{"b目录", "Z目录", "a.TXT", "video.mkv", "中文 名.mp4", "第1集.mkv", "第2集.mkv", "第10集.mkv"}
	if !slices.Equal(got, want) {
		t.Fatalf("Hidden 关:\n got  %q\n want %q", got, want)
	}
	if lr.Total != len(want) {
		t.Errorf("total %d", lr.Total)
	}
	for _, it := range lr.Items {
		wantKind := map[string]string{"b目录": "dir", "Z目录": "dir", "a.TXT": "other", "video.mkv": "video"}[it.Name]
		if wantKind != "" && it.Kind != wantKind {
			t.Errorf("%s kind %s, want %s", it.Name, it.Kind, wantKind)
		}
		if it.HasThumb || it.Progress != nil {
			t.Errorf("%s: P1 hasThumb/progress 应为 false/null", it.Name)
		}
		if it.Kind == "dir" && it.Size != 0 {
			t.Errorf("目录 size 应为 0")
		}
		if it.Name == "video.mkv" && (it.Size != 10 || it.MTime == 0) {
			t.Errorf("video.mkv size/mtime: %d %d", it.Size, it.MTime)
		}
	}

	lr = list(t, s, "tok1", q("/api/list", "priv", ""))
	hidden := map[string]bool{}
	for _, it := range lr.Items {
		hidden[it.Name] = it.Hidden
	}
	for _, n := range []string{".dot", "attrhidden.txt", "hdir"} {
		if h, ok := hidden[n]; !ok || !h {
			t.Errorf("Hidden 开: %s 应列出且 hidden=true (%v %v)", n, ok, h)
		}
	}
	for _, n := range []string{"desktop.ini", "$RECYCLE.BIN", ".lfb-upload-abcd"} {
		if _, ok := hidden[n]; ok {
			t.Errorf("始终隐藏项 %s 不应出现", n)
		}
	}
	if hidden["video.mkv"] {
		t.Error("普通文件 hidden 应为 false")
	}

	// 隐藏项访问：无权时 404，有权时可访问；始终隐藏项一律 404
	for _, p := range []string{".dot", "attrhidden.txt", "hdir/in.txt"} {
		wantErr(t, fget(t, s, "tok1", q("/api/file", "pub", p)), 404, CodeNotFound, "Hidden 关访问 "+p)
		if w := fget(t, s, "tok1", q("/api/file", "priv", p)); w.Code != 200 {
			t.Errorf("Hidden 开访问 %s: %d", p, w.Code)
		}
	}
	wantErr(t, fget(t, s, "tok1", q("/api/list", "pub", "hdir")), 404, CodeNotFound, "Hidden 关列隐藏目录")
	wantErr(t, fget(t, s, "tok1", q("/api/file", "priv", "desktop.ini")), 404, CodeNotFound, "始终隐藏项")
	wantErr(t, fget(t, s, "tok1", q("/api/list", "priv", "$RECYCLE.BIN")), 404, CodeNotFound, "始终隐藏目录")

	// 子目录与「不是目录」
	if got := names(list(t, s, "tok1", q("/api/list", "pub", "Z目录"))); !slices.Equal(got, []string{"x.mp4"}) {
		t.Errorf("子目录: %q", got)
	}
	wantErr(t, fget(t, s, "tok1", q("/api/list", "pub", "video.mkv")), 404, CodeNotFound, "list 文件")
}

func TestListSortAndPaging(t *testing.T) {
	s, root := newFileServer(t)

	got := names(list(t, s, "tok1", q("/api/list", "pub", "", "order", "desc")))
	want := []string{"Z目录", "b目录", "第10集.mkv", "第2集.mkv", "第1集.mkv", "中文 名.mp4", "video.mkv", "a.TXT"}
	if !slices.Equal(got, want) {
		t.Errorf("desc（目录仍在前）:\n got  %q\n want %q", got, want)
	}

	got = names(list(t, s, "tok1", q("/api/list", "pub", "", "sort", "size")))
	want = []string{"b目录", "Z目录", "第1集.mkv", "第2集.mkv", "中文 名.mp4", "第10集.mkv", "a.TXT", "video.mkv"} // 同大小按名称自然排序
	if !slices.Equal(got, want) {
		t.Errorf("size:\n got  %q\n want %q", got, want)
	}

	if err := os.Chtimes(filepath.Join(root, "a.TXT"), timeOld, timeOld); err != nil {
		t.Fatal(err)
	}
	got = names(list(t, s, "tok1", q("/api/list", "pub", "", "sort", "mtime")))
	if len(got) < 3 || got[2] != "a.TXT" {
		t.Errorf("mtime 最旧文件应在目录之后第一位: %q", got)
	}

	lr := list(t, s, "tok1", q("/api/list", "pub", "", "offset", "2", "limit", "3"))
	if got := names(lr); lr.Total != 8 || !slices.Equal(got, []string{"a.TXT", "video.mkv", "中文 名.mp4"}) {
		t.Errorf("分页: total=%d %q", lr.Total, got)
	}
	lr = list(t, s, "tok1", q("/api/list", "pub", "", "offset", "100"))
	if lr.Total != 8 || len(lr.Items) != 0 {
		t.Errorf("越界 offset: total=%d items=%d", lr.Total, len(lr.Items))
	}
	lr = list(t, s, "tok1", q("/api/list", "pub", "", "limit", "100000"))
	if len(lr.Items) != 8 {
		t.Errorf("超大 limit 应截断到上限而非报错: %d", len(lr.Items))
	}
	for _, bad := range [][]string{{"sort", "foo"}, {"order", "up"}, {"offset", "-1"}, {"limit", "0"}, {"limit", "x"}} {
		wantErr(t, fget(t, s, "tok1", q("/api/list", "pub", "", bad...)), 400, CodeBadPath, "参数 "+bad[0]+"="+bad[1])
	}
}

func TestListETag(t *testing.T) {
	s, root := newFileServer(t)
	target := q("/api/list", "pub", "")
	w := fget(t, s, "tok1", target)
	etag := w.Header().Get("ETag")
	if w.Code != 200 || etag == "" {
		t.Fatalf("%d etag=%q", w.Code, etag)
	}
	w = fget(t, s, "tok1", target, "If-None-Match", etag)
	if w.Code != 304 || w.Body.Len() != 0 {
		t.Fatalf("未变应 304: %d", w.Code)
	}
	if w := fget(t, s, "tok1", target, "If-None-Match", `"other", W/`+etag); w.Code != 304 {
		t.Errorf("列表/弱比较应 304: %d", w.Code)
	}
	if err := os.WriteFile(filepath.Join(root, "new.mp4"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := fget(t, s, "tok1", target, "If-None-Match", etag); w.Code != 200 || w.Header().Get("ETag") == etag {
		t.Errorf("新增文件后应 200 且 ETag 改变: %d", w.Code)
	}
	// 隐藏文件不可见时，其变化不影响 ETag
	etag = fget(t, s, "tok1", target).Header().Get("ETag")
	os.WriteFile(filepath.Join(root, ".dot"), []byte("changed!"), 0o644)
	if w := fget(t, s, "tok1", target, "If-None-Match", etag); w.Code != 304 {
		t.Errorf("隐藏文件变化不应影响 ETag: %d", w.Code)
	}
}

func TestFileServe(t *testing.T) {
	s, _ := newFileServer(t)
	w := fget(t, s, "tok1", q("/api/file", "pub", "video.mkv"))
	if w.Code != 200 || w.Body.String() != "0123456789" {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/x-matroska" {
		t.Errorf("Content-Type %q", ct)
	}
	if w.Header().Get("Content-Encoding") != "" || w.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("头: %v", w.Header())
	}

	w = fget(t, s, "tok1", q("/api/file", "pub", "video.mkv"), "Range", "bytes=2-5")
	if w.Code != 206 || w.Body.String() != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Errorf("Range: %d %q %q", w.Code, w.Body, w.Header().Get("Content-Range"))
	}

	lm := w.Header().Get("Last-Modified")
	if w := fget(t, s, "tok1", q("/api/file", "pub", "video.mkv"), "If-Modified-Since", lm); w.Code != 304 {
		t.Errorf("If-Modified-Since: %d", w.Code)
	}

	if w := fget(t, s, "", q("/api/file", "pub", "video.mkv")+"&t=tok1"); w.Code != 200 {
		t.Errorf("?t= 访问: %d", w.Code)
	}
	if w := fget(t, s, "tok1", q("/api/file", "pub", `Z目录\x.mp4`)); w.Code != 200 || w.Header().Get("Content-Type") != "video/mp4" {
		t.Errorf("反斜杠子路径: %d", w.Code)
	}
	wantErr(t, fget(t, s, "tok1", q("/api/file", "pub", "Z目录")), 404, CodeNotFound, "file 目录")
	wantErr(t, fget(t, s, "tok1", q("/api/file", "pub", "")), 404, CodeNotFound, "file 共享根")
	wantErr(t, fget(t, s, "tok1", q("/api/file", "pub", "missing.mkv")), 404, CodeNotFound, "file 不存在")
}

func TestDownload(t *testing.T) {
	s, _ := newFileServer(t)
	wantErr(t, fget(t, s, "tok1", q("/api/download", "pub", "中文 名.mp4")), 403, CodeForbidden, "无 download 权限")

	w := fget(t, s, "tok1", q("/api/download", "priv", "中文 名.mp4"))
	if w.Code != 200 || w.Body.String() != "zh" {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
	want := "attachment; filename*=UTF-8''%E4%B8%AD%E6%96%87%20%E5%90%8D.mp4"
	if cd := w.Header().Get("Content-Disposition"); cd != want {
		t.Errorf("Content-Disposition\n got  %q\n want %q", cd, want)
	}
	if w := fget(t, s, "tok1", q("/api/download", "priv", "video.mkv"), "Range", "bytes=8-"); w.Code != 206 || w.Body.String() != "89" {
		t.Errorf("download Range: %d %q", w.Code, w.Body)
	}
}

func TestPathAttacks(t *testing.T) {
	s, _ := newFileServer(t)
	for _, p := range []string{"../Windows/win.ini", `..\..\x`, "C:/Windows/win.ini", "video.mkv::$DATA", "a\x00.mp4", "CON.txt", "/etc/x", "video.mkv."} {
		for _, ep := range []string{"/api/list", "/api/file"} {
			wantErr(t, fget(t, s, "tok1", q(ep, "priv", p)), 400, CodeBadPath, ep+" "+p)
		}
	}
}

var timeOld = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func mklinkJ(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
}

// 经联接绕过隐藏检查：L → a/.private/x（祖先以 . 开头），M → b/hid/y（祖先带 Hidden 属性）。
func TestLinkToHiddenPath(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a/.private/x/file.txt", "b/hid/y/file.txt", "ok/file.txt"} {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hp, _ := syscall.UTF16PtrFromString(filepath.Join(root, "b", "hid"))
	if err := syscall.SetFileAttributes(hp, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
	mklinkJ(t, filepath.Join(root, "L"), filepath.Join(root, "a", ".private", "x"))
	mklinkJ(t, filepath.Join(root, "M"), filepath.Join(root, "b", "hid", "y"))
	mklinkJ(t, filepath.Join(root, "K"), filepath.Join(root, "ok"))
	s := &Server{Config: config.NewStore(&config.Config{Shares: []config.Share{
		{ID: "pub", Name: "公共", Path: root},
		{ID: "priv", Name: "私有", Path: root, Perms: config.Perms{Hidden: true}},
	}}), Auth: TokenMap{"tok1": "dev1"}}

	got := names(list(t, s, "tok1", q("/api/list", "pub", "")))
	if !slices.Equal(got, []string{"a", "b", "K", "ok"}) {
		t.Errorf("Hidden 关不应列出指向隐藏路径的链接: %q", got)
	}
	for _, l := range []string{"L", "M"} {
		wantErr(t, fget(t, s, "tok1", q("/api/file", "pub", l+"/file.txt")), 404, CodeNotFound, "Hidden 关经链接访问 "+l)
		wantErr(t, fget(t, s, "tok1", q("/api/list", "pub", l)), 404, CodeNotFound, "Hidden 关经链接列目录 "+l)
		if w := fget(t, s, "tok1", q("/api/file", "priv", l+"/file.txt")); w.Code != 200 {
			t.Errorf("Hidden 开经链接访问 %s: %d", l, w.Code)
		}
	}
	if w := fget(t, s, "tok1", q("/api/file", "pub", "K/file.txt")); w.Code != 200 {
		t.Errorf("指向普通目录的链接: %d", w.Code)
	}
	hidden := map[string]bool{}
	for _, it := range list(t, s, "tok1", q("/api/list", "priv", "")).Items {
		hidden[it.Name] = it.Hidden
	}
	if !hidden["L"] || !hidden["M"] || hidden["K"] {
		t.Errorf("Hidden 开时链接 hidden 应按目标判定: %v", hidden)
	}
}
