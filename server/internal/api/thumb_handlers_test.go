package api

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"lfb/server/internal/config"
	"lfb/server/internal/thumb"
)

func newThumbServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, root := newFileServer(t)
	s.Thumbs = thumb.New(t.TempDir(), "")     // 无 ffmpeg
	t.Cleanup(func() { s.Thumbs.WaitIdle() }) // 预热写入缓存目录，须在临时目录删除前结束
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 1000, 400))); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"pic.png", "hdir/h.png", ".hidden.png"} {
		if err := os.WriteFile(filepath.Join(root, rel), b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s, root
}

func TestThumbImage(t *testing.T) {
	s, _ := newThumbServer(t)
	// 原图 1000x400：预览图档位不放大
	for w, want := range map[string]int{"": 320, "100": 160, "300": 320, "600": 640, "641": 1000, "5000": 1000} {
		rec := fget(t, s, "tok1", q("/api/thumb", "pub", "pic.png", "w", w))
		if rec.Code != 200 {
			t.Fatalf("w=%s: %d %s", w, rec.Code, rec.Body)
		}
		if ct, cc := rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"); ct != "image/jpeg" || cc != "private, max-age=86400" {
			t.Errorf("头: %q %q", ct, cc)
		}
		cfg, err := jpeg.DecodeConfig(rec.Body)
		if err != nil || cfg.Width != want {
			t.Errorf("w=%s: 宽 %d, want %d (%v)", w, cfg.Width, want, err)
		}
	}
	// 缓存：同一宽度只生成一个文件
	ents, _ := os.ReadDir(s.Thumbs.Dir)
	if len(ents) != 5 {
		t.Errorf("缓存文件数 %d, want 5（160/320/640/1280/2560）", len(ents))
	}
	wantErr(t, fget(t, s, "tok1", q("/api/thumb", "pub", "pic.png", "w", "abc")), 400, CodeBadPath, "w 非数字")
	wantErr(t, fget(t, s, "tok1", q("/api/thumb", "pub", "pic.png", "w", "-1")), 400, CodeBadPath, "w 负数")
}

func TestThumbNotFoundCases(t *testing.T) {
	s, _ := newThumbServer(t)
	for _, c := range []struct {
		tok, share, path string
		status           int
		code             string
	}{
		{"tok2", "priv", "pic.png", 404, CodeNotFound},             // 不可见共享
		{"tok1", "gone", "pic.png", 503, CodeShareOffline},         // 离线
		{"tok1", "pub", "missing.png", 404, CodeNotFound},          // 不存在
		{"tok1", "pub", ".hidden.png", 404, CodeNotFound},          // 隐藏（无权限）
		{"tok1", "pub", "hdir/h.png", 404, CodeNotFound},           // 隐藏目录下
		{"tok1", "pub", "a.TXT", 404, CodeNotFound},                // 不支持的类型
		{"tok1", "pub", "Z目录", 404, CodeNotFound},                  // 目录
		{"tok1", "pub", "video.mkv", 404, CodeNotFound},            // 视频，无 ffmpeg
		{"tok1", "pub", "../pic.png", 400, CodeBadPath},            // 穿越
		{"tok1", "pub", `..\..\Windows\win.ini`, 400, CodeBadPath}, // 反斜杠穿越
		{"tok1", "pub", "pic.png::$DATA", 400, CodeBadPath},        // 备用数据流
		{"tok1", "pub", "CON.png", 400, CodeBadPath},               // 设备名
		{"tok1", "pub", "desktop.ini", 404, CodeNotFound},          // 始终隐藏
		{"", "pub", "pic.png", 401, CodeUnauthorized},
	} {
		wantErr(t, fget(t, s, c.tok, q("/api/thumb", c.share, c.path)), c.status, c.code, c.share+"/"+c.path)
	}
	// priv 有“显示隐藏文件”权限：隐藏图片可生成
	if w := fget(t, s, "tok1", q("/api/thumb", "priv", ".hidden.png")); w.Code != 200 {
		t.Errorf("有隐藏权限时应可生成: %d", w.Code)
	}
}

// /api/list 的 hasThumb：图片 true；视频仅在 ffmpeg 可用时 true；目录与其他 false。
func TestListHasThumb(t *testing.T) {
	s, _ := newThumbServer(t)
	check := func(want map[string]bool) {
		t.Helper()
		for _, it := range list(t, s, "tok1", q("/api/list", "pub", "")).Items {
			if w, ok := want[it.Name]; ok && it.HasThumb != w {
				t.Errorf("%s hasThumb = %v, want %v", it.Name, it.HasThumb, w)
			}
		}
	}
	check(map[string]bool{"pic.png": true, "video.mkv": false, "a.TXT": false, "Z目录": false})
	s.Thumbs.WaitIdle()
	s.Thumbs = thumb.New(s.Thumbs.Dir, `C:\fake\ffmpeg.exe`)
	check(map[string]bool{"pic.png": true, "video.mkv": true, "a.TXT": false, "Z目录": false})
}

// ETag/If-None-Match → 304（不生成）；ver 与当前 mtime/size 一致时强缓存，不一致时 no-cache。
func TestThumbETagAndVersion(t *testing.T) {
	s, root := newThumbServer(t)
	fi, err := os.Stat(filepath.Join(root, "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	ver := strconv.FormatInt(fi.ModTime().UnixMilli(), 10) + "_" + strconv.FormatInt(fi.Size(), 10)

	rec := fget(t, s, "tok1", q("/api/thumb", "pub", "pic.png", "w", "320", "ver", ver))
	etag := rec.Header().Get("ETag")
	if rec.Code != 200 || etag == "" || rec.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("%d etag=%q cc=%q", rec.Code, etag, rec.Header().Get("Cache-Control"))
	}
	if w := fget(t, s, "tok1", q("/api/thumb", "pub", "pic.png", "w", "320", "ver", "1_2")); w.Header().Get("Cache-Control") != "private, no-cache" {
		t.Errorf("ver 不一致应 no-cache: %q", w.Header().Get("Cache-Control"))
	}

	// 304：清空缓存目录后仍返回 304 且不重新生成
	os.RemoveAll(s.Thumbs.Dir)
	w := fget(t, s, "tok1", q("/api/thumb", "pub", "pic.png", "w", "300"), "If-None-Match", etag)
	if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("ETag") != etag {
		t.Errorf("If-None-Match 应 304: %d %q", w.Code, w.Header().Get("ETag"))
	}
	if _, err := os.Stat(s.Thumbs.Dir); err == nil {
		t.Error("304 不应生成缩略图")
	}
	if w := fget(t, s, "tok1", q("/api/thumb", "pub", "pic.png", "w", "640"), "If-None-Match", etag); w.Code != 200 {
		t.Errorf("不同宽度 ETag 不同，应 200: %d", w.Code)
	}
	// 文件变化 → ETag 变化
	later := fi.ModTime().Add(time.Minute)
	os.Chtimes(filepath.Join(root, "pic.png"), later, later)
	if w := fget(t, s, "tok1", q("/api/thumb", "pub", "pic.png", "w", "320"), "If-None-Match", etag); w.Code != 200 || w.Header().Get("ETag") == etag {
		t.Errorf("文件变化后应 200 且换 ETag: %d", w.Code)
	}
	// 304 也要先过可见性校验
	wantErr(t, fget(t, s, "tok2", q("/api/thumb", "priv", "pic.png"), "If-None-Match", etag), 404, CodeNotFound, "不可见共享带 ETag")
}

// 系统缩略图只收 fsx 校验后的最终真实路径：共享根是联接、路径中含联接时都不出现链接名。
func TestThumbShellGetsFinalPath(t *testing.T) {
	s, root := newThumbServer(t)
	var got []string
	s.Thumbs.JPEG = nil
	s.Thumbs.Shell = func(p string, box int, generate bool) (image.Image, error) {
		got = append(got, p)
		return nil, errors.New("测试：回落 Go 解码")
	}
	real := filepath.Join(root, "真实 目录")
	os.MkdirAll(real, 0o755)
	data, _ := os.ReadFile(filepath.Join(root, "pic.png"))
	os.WriteFile(filepath.Join(real, "x.png"), data, 0o644)
	mklinkJ(t, filepath.Join(root, "J"), real)
	linkRoot := filepath.Join(t.TempDir(), "共享联接")
	mklinkJ(t, linkRoot, root)
	shares := append(slices.Clone(s.Config.Get().Shares), config.Share{ID: "lnk", Name: "联接", Path: linkRoot})
	s.Config = config.NewStore(&config.Config{Shares: shares})

	for _, c := range []struct{ share, path, want string }{
		{"pub", "J/x.png", filepath.Join(real, "x.png")},
		{"lnk", "pic.png", filepath.Join(root, "pic.png")},
	} {
		got = nil
		if w := fget(t, s, "tok1", q("/api/thumb", c.share, c.path, "w", "320")); w.Code != 200 {
			t.Fatalf("%s/%s: %d %s", c.share, c.path, w.Code, w.Body)
		}
		if len(got) != 1 {
			t.Fatalf("%s/%s: Shell 调用 %v", c.share, c.path, got)
		}
		p := got[0]
		fa, _ := os.Stat(p)
		fb, _ := os.Stat(c.want)
		if strings.HasPrefix(p, `\?\`) || strings.Contains(p, `\J\`) || strings.Contains(p, "共享联接") || fa == nil || !os.SameFile(fa, fb) {
			t.Errorf("%s/%s: Shell 收到 %q，应为最终真实路径 %q", c.share, c.path, p, c.want)
		}
	}
	// 隐藏、穿越等在交给 Shell 之前就被拒绝
	got = nil
	fget(t, s, "tok1", q("/api/thumb", "pub", ".hidden.png"))
	fget(t, s, "tok1", q("/api/thumb", "pub", "../pic.png"))
	if len(got) != 0 {
		t.Errorf("被拒绝的路径不应交给 Shell: %v", got)
	}
}

// 视频不出预览图：w=1920 按 640 取整（与 640 同一缓存键）。
func TestThumbVideoNoPreview(t *testing.T) {
	s, _ := newThumbServer(t)
	s.Thumbs.FFmpeg = `C:\fake\ffmpeg.exe`
	a := fget(t, s, "tok1", q("/api/thumb", "pub", "video.mkv", "w", "1920"))
	b := fget(t, s, "tok1", q("/api/thumb", "pub", "video.mkv", "w", "640"))
	if a.Header().Get("ETag") == "" || a.Header().Get("ETag") != b.Header().Get("ETag") {
		t.Errorf("视频 1920 应按 640 处理: %q %q", a.Header().Get("ETag"), b.Header().Get("ETag"))
	}
}

// /api/list 后台预热：不阻塞列表，只为可见图片生成 320 缓存。
func TestListPrefetch(t *testing.T) {
	s, root := newThumbServer(t)
	s.Thumbs.Shell, s.Thumbs.JPEG = nil, nil
	for i := range 5 {
		data, _ := os.ReadFile(filepath.Join(root, "pic.png"))
		os.WriteFile(filepath.Join(root, fmt.Sprintf("p%d.png", i)), data, 0o644)
	}
	t0 := time.Now()
	list(t, s, "tok1", q("/api/list", "pub", ""))
	if d := time.Since(t0); d > time.Second {
		t.Errorf("list 耗时 %v", d)
	}
	s.Thumbs.WaitIdle()
	ents, _ := os.ReadDir(s.Thumbs.Dir)
	if len(ents) != 6 { // pic.png + p0..p4；不含 .hidden.png、hdir/h.png、视频
		t.Errorf("预热缓存 %d 个, want 6", len(ents))
	}
	// 预热的缓存与真实请求命中同一键
	before := len(ents)
	fget(t, s, "tok1", q("/api/thumb", "pub", "p0.png", "w", "320"))
	if ents, _ = os.ReadDir(s.Thumbs.Dir); len(ents) != before {
		t.Error("预热后的 320 请求应命中缓存")
	}
}
