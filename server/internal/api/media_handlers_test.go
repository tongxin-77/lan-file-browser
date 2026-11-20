package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"lfb/server/internal/config"
)

// newMediaServer 构建临时共享（pub 全部可见、无隐藏权限；priv 仅 dev1 可见、有隐藏权限）。
func newMediaServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"剧/Show.S01E01.mkv",
		"剧/Show.S01E02.mkv",
		"剧/Show.S01E03.mkv", // Hidden 属性
		"剧/Show.S01E10.mkv",
		"剧/Show.S01E01.ass",
		"剧/Show.S01E01.chs.srt",
		"剧/Show.S01E01.zh-CN.ass",
		"剧/Show.S01E01.jp.srt", // Hidden 属性
		"剧/.Show.S01E01.srt",
		"剧/Show.S01E01.txt",
		"剧/[字幕组] Show [01].cht.ass",
		"剧/[字幕组] Show [02].ass",
		"剧/Show.S01.chs.srt", // 季级字幕
		"剧/Show.S02.srt",     // 季不同
		"剧/Other.S01.srt",    // 剧名不同
		"杂/alpha.mp4",
		"杂/beta.mkv",
		"杂/gamma.txt",
		"杂/zeta.avi",
		".hid/x.mkv",
	}
	for _, rel := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"剧/Show.S01E03.mkv", "剧/Show.S01E01.jp.srt"} {
		p, _ := syscall.UTF16PtrFromString(filepath.Join(root, rel))
		if err := syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Shares: []config.Share{
		{ID: "pub", Name: "公共", Path: root},
		{ID: "priv", Name: "私有", Path: root, Devices: []string{"dev1"}, Perms: config.Perms{Hidden: true}},
	}}
	return &Server{Config: config.NewStore(cfg), Auth: TokenMap{"tok1": "dev1", "tok2": "dev2"}}
}

type nextResp struct {
	Item *struct {
		Name string
		Path string
		Kind string
		Size int64
	}
}

func getNext(t *testing.T, s *Server, tok, share, p string) nextResp {
	t.Helper()
	w := fget(t, s, tok, q("/api/next", share, p))
	if w.Code != 200 {
		t.Fatalf("next %s %s: %d %s", share, p, w.Code, w.Body)
	}
	var nr nextResp
	if err := json.Unmarshal(w.Body.Bytes(), &nr); err != nil {
		t.Fatal(err)
	}
	return nr
}

func TestNext(t *testing.T) {
	s := newMediaServer(t)
	cases := []struct {
		tok, share, path, want string // want 为空表示 item 为 null
	}{
		{"tok2", "pub", "剧/Show.S01E01.mkv", "剧/Show.S01E02.mkv"},
		{"tok2", "pub", "剧/Show.S01E02.mkv", "剧/Show.S01E10.mkv"},  // 跳过隐藏的 E03
		{"tok1", "priv", "剧/Show.S01E02.mkv", "剧/Show.S01E03.mkv"}, // 有隐藏权限
		{"tok2", "pub", "剧/Show.S01E10.mkv", ""},                   // 最后一集
		{"tok2", "pub", "杂/alpha.mp4", "杂/beta.mkv"},               // 无集号：自然排序
		{"tok2", "pub", "杂/beta.mkv", "杂/zeta.avi"},                // 跳过非视频
		{"tok2", "pub", "杂/zeta.avi", ""},
	}
	for _, c := range cases {
		nr := getNext(t, s, c.tok, c.share, c.path)
		got := ""
		if nr.Item != nil {
			got = nr.Item.Path
			if nr.Item.Name != filepath.Base(got) || nr.Item.Kind != "video" || nr.Item.Size == 0 {
				t.Errorf("next %s: item %+v", c.path, *nr.Item)
			}
		}
		if got != c.want {
			t.Errorf("next %s %s = %q, want %q", c.share, c.path, got, c.want)
		}
	}
	if w := fget(t, s, "tok2", q("/api/next", "pub", "剧/Show.S01E10.mkv")); w.Body.String() != "{\"item\":null}\n" {
		t.Errorf("last episode body %q", w.Body)
	}
}

type subsResp struct {
	Items []struct {
		Name, Path, Format, Lang, Match string
	}
}

func getSubs(t *testing.T, s *Server, tok, share, p string) subsResp {
	t.Helper()
	w := fget(t, s, tok, q("/api/subtitles", share, p))
	if w.Code != 200 {
		t.Fatalf("subtitles %s %s: %d %s", share, p, w.Code, w.Body)
	}
	var sr subsResp
	if err := json.Unmarshal(w.Body.Bytes(), &sr); err != nil {
		t.Fatal(err)
	}
	return sr
}

func TestSubtitles(t *testing.T) {
	s := newMediaServer(t)
	sr := getSubs(t, s, "tok2", "pub", "剧/Show.S01E01.mkv")
	var got []string
	for _, it := range sr.Items {
		got = append(got, it.Match+"|"+it.Lang+"|"+it.Format+"|"+it.Path)
		if it.Name != filepath.Base(it.Path) {
			t.Errorf("name %q path %q", it.Name, it.Path)
		}
	}
	want := []string{
		"exact||ass|剧/Show.S01E01.ass",
		"exact|chs|srt|剧/Show.S01E01.chs.srt",
		"exact|zh-CN|ass|剧/Show.S01E01.zh-CN.ass",
		"episode|cht|ass|剧/[字幕组] Show [01].cht.ass",
		"season|chs|srt|剧/Show.S01.chs.srt",
	}
	if !slices.Equal(got, want) {
		t.Errorf("subtitles = %q\nwant %q", got, want)
	}

	// 有隐藏权限时隐藏字幕可见
	sr = getSubs(t, s, "tok1", "priv", "剧/Show.S01E01.mkv")
	var names []string
	for _, it := range sr.Items {
		names = append(names, it.Name)
	}
	if !slices.Contains(names, "Show.S01E01.jp.srt") {
		t.Errorf("priv subtitles %q 缺少隐藏字幕", names)
	}

	// 无字幕：空数组而非 null
	if w := fget(t, s, "tok2", q("/api/subtitles", "pub", "杂/alpha.mp4")); w.Body.String() != "{\"items\":[]}\n" {
		t.Errorf("no subtitles body %q", w.Body)
	}
}

func TestMediaErrors(t *testing.T) {
	s := newMediaServer(t)
	cases := []struct {
		tok, share, path string
		want             int
	}{
		{"tok2", "priv", "剧/Show.S01E01.mkv", 404}, // 不可见共享
		{"tok2", "nope", "剧/Show.S01E01.mkv", 404},
		{"tok2", "pub", "../x.mkv", 400},
		{"tok2", "pub", "剧/../../x.mkv", 400},
		{"tok2", "pub", `..\..\Windows\win.ini`, 400},
		{"tok2", "pub", "C:/Windows/win.ini", 400},
		{"tok2", "pub", ".hid/x.mkv", 404},        // 隐藏目录
		{"tok2", "pub", "剧/Show.S01E03.mkv", 404}, // 隐藏文件
		{"tok2", "pub", "剧/none.mkv", 404},
		{"tok2", "pub", "剧", 404}, // 目录
	}
	for _, ep := range []string{"/api/next", "/api/subtitles"} {
		for _, c := range cases {
			if w := fget(t, s, c.tok, q(ep, c.share, c.path)); w.Code != c.want {
				t.Errorf("%s %s %q: %d, want %d", ep, c.share, c.path, w.Code, c.want)
			}
		}
	}
}

// writeFiles 在 root 下创建文件（内容为相对路径）。
func writeFiles(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, rel := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 同目录混放多部剧时只在同名剧集中找下一集；其他视频都无集号时回退自然排序。
func TestNextSameShowAndFallback(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root,
		"混/Alpha.S01E01.mkv",
		"混/Alpha.S01E02.mkv",
		"混/Beta.S01E03.mkv",
		"混/ALPHA.S01E05.mkv", // 剧名大小写不同仍算同一部
		"混/Beta.S01E01.mkv",
		"单/Show.E01.mkv",
		"单/trailer.mkv",
		"单/notes.txt",
		"末/Show.E01.mkv",
		"末/Other.E02.mkv",
	)
	s := &Server{Config: config.NewStore(&config.Config{Shares: []config.Share{{ID: "pub", Name: "公共", Path: root}}}),
		Auth: TokenMap{"tok": "dev"}}
	for _, c := range []struct{ path, want string }{
		{"混/Alpha.S01E01.mkv", "混/Alpha.S01E02.mkv"},
		{"混/Alpha.S01E02.mkv", "混/ALPHA.S01E05.mkv"}, // 不跳到 Beta.S01E03
		{"混/ALPHA.S01E05.mkv", ""},
		{"混/Beta.S01E01.mkv", "混/Beta.S01E03.mkv"},
		{"单/Show.E01.mkv", "单/trailer.mkv"}, // 其他视频都无集号：自然排序
		{"末/Show.E01.mkv", ""},              // 其他视频有集号但不是同一部剧
	} {
		got := ""
		if nr := getNext(t, s, "tok", "pub", c.path); nr.Item != nil {
			got = nr.Item.Path
		}
		if got != c.want {
			t.Errorf("next %s = %q, want %q", c.path, got, c.want)
		}
	}
}
