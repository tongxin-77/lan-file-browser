package thumb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestWidth(t *testing.T) {
	for in, want := range map[int]int{1: 160, 160: 160, 161: 320, 320: 320, 500: 640, 640: 640, 641: 1280, 1500: 1920, 1920: 1920, 2000: 2560, 5000: 2560} {
		if got := Width(in); got != want {
			t.Errorf("Width(%d) = %d, want %d", in, got, want)
		}
	}
	// 预览图只对图片生效：视频最多 640
	for name, want := range map[string]int{"a.jpg": 2560, "b.HEIC": 2560, "v.mkv": 640, "v.MP4": 640} {
		if got := WidthFor(name, 3000); got != want {
			t.Errorf("WidthFor(%s) = %d, want %d", name, got, want)
		}
	}
	if WidthFor("v.mkv", 200) != 320 {
		t.Error("视频小尺寸应照常取整")
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func src(name string, data []byte) Source {
	return Source{Name: name, RealPath: `D:\pics\` + name, ModTime: time.Unix(1700000000, 0), Size: int64(len(data)), File: bytes.NewReader(data)}
}

func decodeJPEG(t *testing.T, p string) image.Config {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestImageThumbAndCacheHit(t *testing.T) {
	s := New(t.TempDir(), "")
	data := pngBytes(t, 1000, 500)
	p, err := s.Get(context.Background(), src("a.png", data), 320)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, p); cfg.Width != 320 || cfg.Height != 160 {
		t.Fatalf("尺寸 %dx%d, want 320x160", cfg.Width, cfg.Height)
	}
	st1, _ := os.Stat(p)

	// 缓存命中：源不可读也能返回同一文件，且不重写
	hit := src("a.png", data)
	hit.File = nil
	p2, err := s.Get(context.Background(), hit, 320)
	if err != nil || p2 != p {
		t.Fatalf("缓存未命中: %v %s", err, p2)
	}
	if st2, _ := os.Stat(p2); !st2.ModTime().Equal(st1.ModTime()) {
		t.Error("缓存命中时不应重新生成")
	}

	// mtime/size/宽度变化 → 新键
	changed := src("a.png", data)
	changed.ModTime = changed.ModTime.Add(time.Second)
	if s.Path(changed, 320) == p || s.Path(src("a.png", data), 640) == p {
		t.Error("mtime 或宽度变化应换键")
	}

	// 小图不放大
	p3, err := s.Get(context.Background(), src("small.png", pngBytes(t, 100, 50)), 640)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, p3); cfg.Width != 100 || cfg.Height != 50 {
		t.Errorf("小图 %dx%d, want 100x50", cfg.Width, cfg.Height)
	}
}

// 解码前用 DecodeConfig 检查像素数，超限拒绝（不解码像素）。
func TestDecodeConfigLimit(t *testing.T) {
	s := New(t.TempDir(), "")
	s.MaxPixels = 100 * 100
	if _, err := s.Get(context.Background(), src("big.png", pngBytes(t, 101, 100)), 160); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("应拒绝超限图片: %v", err)
	}
	if _, err := s.Get(context.Background(), src("ok.png", pngBytes(t, 100, 100)), 160); err != nil {
		t.Fatalf("未超限应成功: %v", err)
	}
	// 头部声明超大尺寸的伪造 PNG：只有头，没有像素数据
	fake := pngBytes(t, 1, 1)
	fake[16], fake[17], fake[18], fake[19] = 0, 1, 0, 0 // 宽 65536
	fake[20], fake[21], fake[22], fake[23] = 0, 1, 0, 0 // 高 65536
	binary.BigEndian.PutUint32(fake[29:], crc32.ChecksumIEEE(fake[12:29]))
	s.MaxPixels = 0
	if _, err := s.Get(context.Background(), src("bomb.png", fake), 160); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("应拒绝声明超大尺寸的图片: %v", err)
	}
}

func TestUnsupportedAndNoFFmpeg(t *testing.T) {
	s := New(t.TempDir(), "")
	s.HEIF = false
	for name, want := range map[string]bool{"a.JPG": true, "b.webp": true, "c.gif": true, "d.heic": false, "e.bmp": false, "f.mkv": false, "g.txt": false} {
		if got := s.Can(name); got != want {
			t.Errorf("Can(%s) = %v, want %v", name, got, want)
		}
	}
	if _, err := s.Get(context.Background(), src("v.mkv", []byte("x")), 320); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("无 ffmpeg 时视频应不支持: %v", err)
	}
	withFF := New(t.TempDir(), `C:\tools\ffmpeg.exe`)
	if !withFF.Can("v.mkv") || withFF.Can("x.txt") {
		t.Error("有 ffmpeg 时视频应可生成")
	}
	var nilSvc *Service
	if nilSvc.Can("a.jpg") {
		t.Error("nil Service 不应可生成")
	}
}

// ffmpeg 参数：路径作为单独参数并带 file: 前缀，不经 shell 拼接。
func TestFFmpegArgs(t *testing.T) {
	path := `D:\视频\a "b" & -x.mkv`
	args := ffmpegArgs(path, 10, 320)
	i := slices.Index(args, "-i")
	if i < 0 || args[i+1] != "file:"+path {
		t.Fatalf("输入参数 %q", args)
	}
	if args[len(args)-1] != "pipe:1" || !slices.Contains(args, "-nostdin") {
		t.Errorf("参数 %q", args)
	}
}

// 同键并发请求合并且全部成功；图片并发上限 min(CPU 核数, 4)（至少 2），视频 2。
func TestConcurrentSameKey(t *testing.T) {
	s := New(t.TempDir(), "")
	data := pngBytes(t, 800, 600)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Get(context.Background(), src("same.png", data), 160)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if want := min(max(runtime.NumCPU(), 2), 4); cap(s.imgSem) != want || cap(s.vidSem) != 2 {
		t.Errorf("并发上限 图片 %d 视频 %d, want %d / 2", cap(s.imgSem), cap(s.vidSem), want)
	}
	ents, _ := os.ReadDir(s.Dir)
	if len(ents) != 1 {
		t.Errorf("缓存目录应只有 1 个文件，实际 %d", len(ents))
	}
}

// 有 ffmpeg 时的真实截帧（片长 2 秒 < 10 秒，验证回退到第 0 秒）；无 ffmpeg 跳过。
func TestVideoWithFFmpeg(t *testing.T) {
	ff := FindFFmpeg()
	if ff == "" {
		t.Skip("PATH 中无 ffmpeg")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "测试 视频.mp4")
	gen := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=10",
		"-c:v", "mpeg4", "-y", video)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试视频: %v %s", err, out)
	}
	fi, err := os.Stat(video)
	if err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(dir, "cache"), ff)
	p, err := s.Get(context.Background(), Source{Name: fi.Name(), RealPath: video, ModTime: fi.ModTime(), Size: fi.Size()}, 160)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, p); cfg.Width != 160 {
		t.Errorf("视频缩略图宽 %d, want 160", cfg.Width)
	}
}
