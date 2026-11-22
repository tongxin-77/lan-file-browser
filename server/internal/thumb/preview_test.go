package thumb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	red    = color.RGBA{255, 0, 0, 255}
	green  = color.RGBA{0, 255, 0, 255}
	blue   = color.RGBA{0, 0, 255, 255}
	yellow = color.RGBA{255, 255, 0, 255}
	gray   = color.RGBA{128, 128, 128, 255}
)

// quadrants：左上红、右上绿、左下蓝、右下黄。
func quadrants(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := red
			switch {
			case x >= w/2 && y < h/2:
				c = green
			case x < w/2 && y >= h/2:
				c = blue
			case x >= w/2 && y >= h/2:
				c = yellow
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

func jpegOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// exifJPEG 在 JPEG 的 SOI 后插入 APP1 Exif：IFD0 含 Orientation；thumb 非空时 IFD1 指向内嵌缩略图。
func exifJPEG(t *testing.T, img image.Image, orient int, thumb []byte) []byte {
	t.Helper()
	bo := binary.BigEndian // 用 MM 字节序，覆盖与生成器不同的分支
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, bo, v) }
	b.WriteString("MM")
	w(uint16(42))
	w(uint32(8))
	w(uint16(1))
	w([]uint16{0x0112, 3})
	w(uint32(1))
	w([]uint16{uint16(orient), 0})
	if thumb == nil {
		w(uint32(0))
	} else {
		ifd1 := uint32(8 + 2 + 12 + 4)
		w(ifd1)
		w(uint16(2))
		w([]uint16{0x0201, 4})
		w([]uint32{1, ifd1 + 2 + 24 + 4})
		w([]uint16{0x0202, 4})
		w([]uint32{1, uint32(len(thumb))})
		w(uint32(0))
		b.Write(thumb)
	}
	seg := append([]byte("Exif\x00\x00"), b.Bytes()...)
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(seg)+2))
	main := jpegOf(t, img)
	out := append([]byte{0xFF, 0xD8}, app1...)
	out = append(out, seg...)
	return append(out, main[2:]...)
}

func closest(c color.Color) color.RGBA {
	r, g, b, _ := c.RGBA()
	best, bd := red, -1
	for _, p := range []color.RGBA{red, green, blue, yellow, gray} {
		d := sq(int(r>>8)-int(p.R)) + sq(int(g>>8)-int(p.G)) + sq(int(b>>8)-int(p.B))
		if bd < 0 || d < bd {
			best, bd = p, d
		}
	}
	return best
}

func sq(x int) int { return x * x }

func readJPEG(t *testing.T, p string) image.Image {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// noNative 关闭系统缩略图与 WIC，只测 Go 路径。
func noNative(s *Service) *Service {
	s.Shell, s.JPEG, s.HEIF = nil, nil, false
	return s
}

// 各 EXIF Orientation 输出转正：原始像素为四色象限，按 EXIF 规范检查输出左上、右上颜色与宽高。
// Go 解码与 WIC 按比例解码两条路径都测。
func TestOrientation(t *testing.T) {
	want := map[int][2]color.RGBA{ // 输出左上、右上
		1: {red, green}, 2: {green, red}, 3: {yellow, blue}, 4: {blue, yellow},
		5: {red, blue}, 6: {blue, red}, 7: {yellow, green}, 8: {green, yellow},
	}
	dir := t.TempDir()
	for _, path := range []string{"go", "wic"} {
		for o := 1; o <= 8; o++ {
			s := noNative(New(t.TempDir(), ""))
			data := exifJPEG(t, quadrants(800, 400), o, nil)
			sc := src("o.jpg", data)
			if path == "wic" {
				s.JPEG = DecodeJPEGScaled
				fp := filepath.Join(dir, path+string(rune('0'+o))+".jpg")
				os.WriteFile(fp, data, 0o644)
				f, err := os.Open(fp)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				sc.File = f
			}
			p, err := s.Get(context.Background(), sc, 160)
			if err != nil {
				t.Fatalf("%s o=%d: %v", path, o, err)
			}
			img := readJPEG(t, p)
			b := img.Bounds()
			ww, wh := 160, 80
			if o >= 5 {
				ww, wh = 160, 320
			}
			if b.Dx() != ww || b.Dy() != wh {
				t.Errorf("%s o=%d: 尺寸 %v, want %dx%d", path, o, b.Size(), ww, wh)
			}
			tl, tr := closest(img.At(b.Dx()/8, b.Dy()/8)), closest(img.At(b.Dx()*7/8, b.Dy()/8))
			if tl != want[o][0] || tr != want[o][1] {
				t.Errorf("%s o=%d: 左上 %v 右上 %v, want %v %v", path, o, tl, tr, want[o][0], want[o][1])
			}
		}
	}
}

// w=160 时取用尺寸足够、比例一致的内嵌缩略图（并按方向转正）；其他宽度、比例不符、尺寸不足时不用。
func TestExifThumbnail(t *testing.T) {
	thumb := jpegOf(t, solid(160, 120, red))
	main := solid(800, 600, gray)
	get := func(data []byte, w int) image.Image {
		t.Helper()
		s := noNative(New(t.TempDir(), ""))
		p, err := s.Get(context.Background(), src("e.jpg", data), w)
		if err != nil {
			t.Fatal(err)
		}
		return readJPEG(t, p)
	}
	if img := get(exifJPEG(t, main, 1, thumb), 160); closest(img.At(80, 60)) != red || img.Bounds().Dx() != 160 {
		t.Errorf("160 应使用内嵌缩略图: %v %v", closest(img.At(80, 60)), img.Bounds())
	}
	if img := get(exifJPEG(t, main, 1, thumb), 320); closest(img.At(80, 60)) != gray {
		t.Error("320 不应使用内嵌缩略图")
	}
	if img := get(exifJPEG(t, main, 6, thumb), 160); img.Bounds().Dx() != 160 || img.Bounds().Dy() != 213 {
		t.Errorf("Orientation=6 应转正: %v", img.Bounds())
	} else if closest(img.At(80, 100)) != gray {
		t.Error("竖图需要 160x213，内嵌缩略图转正后仅 120 宽，不应使用")
	}
	if img := get(exifJPEG(t, solid(900, 400, gray), 1, thumb), 160); closest(img.At(80, 30)) != gray {
		t.Error("比例不一致（如带黑边）不应使用")
	}
	if img := get(exifJPEG(t, main, 1, jpegOf(t, solid(96, 72, red))), 160); closest(img.At(80, 60)) != gray {
		t.Error("尺寸不足不应使用")
	}
}

// 预览图：不放大，质量 85；视频取整不超过 640（WidthFor 已测）。
func TestPreviewNoUpscale(t *testing.T) {
	s := noNative(New(t.TempDir(), ""))
	p, err := s.Get(context.Background(), src("p.png", pngBytes(t, 1000, 400)), 1920)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, p); cfg.Width != 1000 || cfg.Height != 400 {
		t.Errorf("预览图不应放大: %dx%d", cfg.Width, cfg.Height)
	}
	p, err = s.Get(context.Background(), src("big.png", pngBytes(t, 3000, 1000)), 1280)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, p); cfg.Width != 1280 || cfg.Height != 426 {
		t.Errorf("预览图 %dx%d, want 1280x426", cfg.Width, cfg.Height)
	}
	if quality(1280) != 85 || quality(640) != 80 {
		t.Error("质量：预览 85、缩略图 80")
	}
}

type shellCall struct {
	path     string
	box      int
	generate bool
}

// 系统缩略图：≤640 时优先使用（JPEG 只查缓存，其他格式允许生成）；预览图不用；失败或尺寸不足时回落 Go 解码。
func TestShellAndFallback(t *testing.T) {
	var calls []shellCall
	var fail, tiny bool
	s := noNative(New(t.TempDir(), ""))
	s.Shell = func(path string, box int, generate bool) (image.Image, error) {
		calls = append(calls, shellCall{path, box, generate})
		switch {
		case fail:
			return nil, errors.New("无缩略图")
		case tiny:
			return solid(box/4, box/8, red), nil
		}
		return solid(box, box/2, red), nil
	}
	get := func(name string, data []byte, w int) color.RGBA {
		t.Helper()
		calls = nil
		sc := src(name, data)
		sc.ModTime = sc.ModTime.Add(time.Duration(len(calls)+w) * time.Second) // 每次换键，避免缓存命中
		sc.Size += int64(w)
		if fail {
			sc.Size++
		}
		if tiny {
			sc.Size += 2
		}
		p, err := s.Get(context.Background(), sc, w)
		if err != nil {
			t.Fatal(err)
		}
		img := readJPEG(t, p)
		return closest(img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2))
	}
	pngData := solidPNG(t, 1000, 500, gray)
	if c := get("a.png", pngData, 320); c != red || len(calls) != 1 || calls[0] != (shellCall{`D:\pics\a.png`, 320, true}) {
		t.Errorf("PNG 320 应用系统缩略图（允许生成）: %v %+v", c, calls)
	}
	if c := get("a.jpg", jpegOf(t, solid(1000, 500, gray)), 320); c != red || len(calls) != 1 || calls[0].generate {
		t.Errorf("JPEG 320 应只查系统缓存: %v %+v", c, calls)
	}
	if c := get("a.png", pngData, 1280); c != gray || len(calls) != 0 {
		t.Errorf("预览图不应用系统缩略图: %v %+v", c, calls)
	}
	fail = true
	if c := get("a.png", pngData, 320); c != gray || len(calls) != 1 {
		t.Errorf("系统缩略图失败应回落 Go 解码: %v %+v", c, calls)
	}
	fail, tiny = false, true
	if c := get("a.png", pngData, 640); c != gray {
		t.Errorf("系统缩略图尺寸不足应回落: %v", c)
	}
}

func solidPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, solid(w, h, c)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// HEIC：仅系统能解码且启用 Shell 时可生成；尺寸未知，竖图宽度不足时放大框重取。
func TestHEIC(t *testing.T) {
	s := noNative(New(t.TempDir(), ""))
	if s.Can("a.heic") {
		t.Error("无 HEIF 解码器不应可生成")
	}
	s.HEIF = true
	if s.Can("a.heic") {
		t.Error("无 Shell 不应可生成")
	}
	var boxes []int
	s.Shell = func(path string, box int, generate bool) (image.Image, error) {
		boxes = append(boxes, box)
		if !generate {
			t.Error("HEIC 应允许系统生成")
		}
		return solid(box*2/3, box, red), nil // 2:3 竖图，最长边 = box
	}
	if !s.Can("a.HEIF") || s.Can("a.bmp") {
		t.Error("Can")
	}
	p, err := s.Get(context.Background(), Source{Name: "a.heic", RealPath: `D:\pics\a.heic`, ModTime: time.Unix(1, 0), Size: 1}, 160)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, p); cfg.Width != 160 || len(boxes) != 2 {
		t.Errorf("HEIC 竖图宽 %d（框 %v），want 160", cfg.Width, boxes)
	}
	s.Shell = func(string, int, bool) (image.Image, error) { return nil, errors.New("缺少 HEVC 扩展") }
	if _, err := s.Get(context.Background(), Source{Name: "b.heic", RealPath: `D:\pics\b.heic`, Size: 2}, 160); err == nil {
		t.Error("系统无法出图时应报错（接口返回 404）")
	}
}

// 真实系统缩略图（若环境支持）：返回结果已按 EXIF 转正、不放大。
func TestShellThumbnailReal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "竖拍 照片.jpg")
	os.WriteFile(p, exifJPEG(t, quadrants(800, 400), 6, nil), 0o644)
	img, err := ShellThumbnail(p, 160, true)
	if err != nil {
		t.Skipf("系统缩略图不可用: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 80 || b.Dy() != 160 {
		t.Errorf("系统缩略图应已转正: %v", b)
	}
	if _, err := ShellThumbnail(filepath.Join(dir, "不存在.jpg"), 160, true); err == nil {
		t.Error("不存在的文件应失败")
	}
}

// 淘汰：超过上限按 mtime 从旧到新删到 90% 以下；清理过期临时文件；命中时刷新访问时间。
func TestPrune(t *testing.T) {
	dir := t.TempDir()
	s := noNative(New(dir, ""))
	s.MaxBytes = 250
	base := time.Now().Add(-10 * time.Hour)
	for i, n := range []string{"a.jpg", "b.jpg", "c.jpg", "d.jpg", "tmp-old", "tmp-new"} {
		p := filepath.Join(dir, n)
		os.WriteFile(p, bytes.Repeat([]byte{1}, 100), 0o600)
		mt := base.Add(time.Duration(i) * time.Hour)
		if n == "tmp-new" {
			mt = time.Now()
		}
		os.Chtimes(p, mt, mt)
	}
	s.Prune()
	var left []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != "c.jpg,d.jpg,tmp-new" || s.total.Load() != 200 {
		t.Errorf("剩余 %v，统计 %d", left, s.total.Load())
	}

	// 写入时超限触发淘汰
	s2 := noNative(New(t.TempDir(), ""))
	first, err := s2.Get(context.Background(), src("p.png", pngBytes(t, 600, 300)), 640)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(first)
	limit := st.Size()*3 + st.Size()/2
	s2.MaxBytes = limit
	sizes := st.Size()
	for i := 1; i < 6; i++ {
		sc := src("p.png", pngBytes(t, 600, 300))
		sc.Size += int64(i)
		p, err := s2.Get(context.Background(), sc, 640)
		if err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(p)
		sizes += st.Size()
	}
	var total int64
	ents, _ = os.ReadDir(s2.Dir)
	for _, e := range ents {
		fi, _ := e.Info()
		total += fi.Size()
	}
	if sizes <= limit || total > limit || total == 0 {
		t.Errorf("写入合计 %d，淘汰后 %d，上限 %d", sizes, total, limit)
	}

	// 命中刷新访问时间
	sc := src("hit.png", pngBytes(t, 50, 50))
	p, err := s2.Get(context.Background(), sc, 160)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(p, old, old)
	sc.File = nil
	if _, err := s2.Get(context.Background(), sc, 160); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); time.Since(st.ModTime()) > time.Minute {
		t.Error("命中应刷新访问时间")
	}
}

// 预热：立即返回、最多 MaxPrefetch 个、新队列替换旧队列、真实请求进行中时让路。
func TestPrefetch(t *testing.T) {
	s := noNative(New(t.TempDir(), ""))
	block, started := make(chan struct{}), make(chan struct{})
	var ran atomic.Int32
	jobs := []func(context.Context){func(context.Context) { close(started); <-block }}
	for range 300 {
		jobs = append(jobs, func(context.Context) { ran.Add(1) })
	}
	t0 := time.Now()
	s.Prefetch(jobs)
	if d := time.Since(t0); d > 50*time.Millisecond {
		t.Errorf("Prefetch 应立即返回: %v", d)
	}
	<-started
	s.prefetch.Lock()
	n := len(s.prefetch.jobs)
	s.prefetch.Unlock()
	if n != MaxPrefetch-1 {
		t.Errorf("队列 %d, want %d", n, MaxPrefetch-1)
	}
	var newRan atomic.Int32
	s.Prefetch([]func(context.Context){func(context.Context) { newRan.Add(1) }, func(context.Context) { newRan.Add(1) }})
	close(block)
	s.WaitIdle()
	if ran.Load() != 0 || newRan.Load() != 2 {
		t.Errorf("旧队列剩余应丢弃: 旧 %d 新 %d", ran.Load(), newRan.Load())
	}

	// 真实请求进行中时让路
	s.active.Add(1)
	var done atomic.Bool
	s.Prefetch([]func(context.Context){func(context.Context) { done.Store(true) }})
	time.Sleep(100 * time.Millisecond)
	if done.Load() {
		t.Error("有真实请求时预热不应执行")
	}
	s.active.Add(-1)
	s.WaitIdle()
	if !done.Load() {
		t.Error("真实请求结束后应继续预热")
	}

	// Warm 实际生成 320 缓存
	sc := src("w.png", pngBytes(t, 640, 320))
	if err := s.Warm(context.Background(), sc, PrefetchWidth); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path(sc, PrefetchWidth)); err != nil {
		t.Error("Warm 应生成缓存")
	}
}

func TestETagStable(t *testing.T) {
	s := New(t.TempDir(), "")
	a := src("a.png", []byte("x"))
	if s.ETag(a, 320) != s.ETag(a, 320) || s.ETag(a, 320) == s.ETag(a, 640) {
		t.Error("ETag 应随宽度变化、同源同宽稳定")
	}
	b := a
	b.ModTime = b.ModTime.Add(time.Millisecond)
	if s.ETag(a, 320) == s.ETag(b, 320) {
		t.Error("mtime 变化应换 ETag")
	}
}
