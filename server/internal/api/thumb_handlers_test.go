package api

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"lfb/server/internal/thumb"
)

func newThumbServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, root := newFileServer(t)
	s.Thumbs = thumb.New(t.TempDir(), "") // 无 ffmpeg
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
	for w, want := range map[string]int{"": 320, "100": 160, "300": 320, "641": 640, "5000": 640} {
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
	if len(ents) != 3 {
		t.Errorf("缓存文件数 %d, want 3（160/320/640）", len(ents))
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
	s.Thumbs = thumb.New(s.Thumbs.Dir, `C:\fake\ffmpeg.exe`)
	check(map[string]bool{"pic.png": true, "video.mkv": true, "a.TXT": false, "Z目录": false})
}
