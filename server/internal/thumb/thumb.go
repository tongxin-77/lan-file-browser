// Package thumb 生成并缓存缩略图与预览图（03 文档 4.5）。
//
// 宽度分档 160/320/640（缩略图）与 1280/1920/2560（预览图，仅图片；视频最多 640）。图片按 EXIF
// Orientation 转正，不放大；出图来源依次为：JPEG 内嵌 EXIF 缩略图（仅 160）→ Windows 系统缩略图
// （≤640；JPEG 只取系统缓存）→ WIC 按比例解码 JPEG → Go 图像库解码。视频仅在找到 ffmpeg 时截取一帧。
// 输出一律 JPEG，缓存在磁盘目录中，键为 sha256(版本 + 真实路径 + mtime + size + 宽度)，文件变更后键
// 自然失效；总量超过 MaxBytes 时按最近访问时间（以缓存文件 mtime 记录）淘汰。
// 图片生成并发上限 min(CPU 核数, 4)，视频（ffmpeg）2；同键并发请求合并为一次生成。
// 调用方负责路径安全与可见性校验。
package thumb

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // 注册解码器
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"lfb/server/internal/media"
)

var (
	ErrUnsupported = errors.New("thumb: 不支持的类型")
	ErrTooLarge    = errors.New("thumb: 图片尺寸超限")
)

const (
	DefaultMaxPixels = 50_000_000 // 解码前按 DecodeConfig 检查，防内存炸弹
	DefaultMaxBytes  = 1 << 30    // 缓存目录上限 1 GB
	MaxPrefetch      = 200        // 每次预热最多处理的文件数
	PrefetchWidth    = 320
	maxThumbWidth    = 640 // 视频与系统缩略图的上限；更大为预览图
	ffmpegTimeout    = 20 * time.Second
	maxFFmpegOutput  = 8 << 20
	jpegQuality      = 80
	previewQuality   = 85
	cacheVersion     = "v2" // 出图算法变化（如 EXIF 转正）时递增，使旧缓存失效
	touchInterval    = time.Hour
)

var widths = []int{160, 320, 640, 1280, 1920, 2560}

var goExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}
var heifExts = map[string]bool{".heic": true, ".heif": true}

func ext(name string) string { return strings.ToLower(filepath.Ext(name)) }
func isJPEG(name string) bool {
	e := ext(name)
	return e == ".jpg" || e == ".jpeg"
}

// Width 把请求宽度向上取整到 160/320/640/1280/1920/2560，超过 2560 取 2560。
func Width(w int) int {
	for _, x := range widths {
		if w <= x {
			return x
		}
	}
	return widths[len(widths)-1]
}

// WidthFor 同 Width，但视频最多 640（预览图只对图片生效）。
func WidthFor(name string, w int) int {
	w = Width(w)
	if media.Kind(name) == media.KindVideo {
		w = min(w, maxThumbWidth)
	}
	return w
}

// FindFFmpeg 返回 PATH 中 ffmpeg 的路径，找不到返回空串。
func FindFFmpeg() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

// Service 并发安全。
type Service struct {
	Dir       string // 缓存目录
	FFmpeg    string // ffmpeg 可执行文件路径；空 = 不生成视频缩略图
	MaxPixels int64  // 0 = DefaultMaxPixels
	MaxBytes  int64  // 缓存上限；0 = DefaultMaxBytes

	// Shell 取系统缩略图（最长边不超过 box；generate=false 时只查系统缓存）；nil = 不用。
	Shell func(path string, box int, generate bool) (image.Image, error)
	// JPEG 按比例快速解码 JPEG，结果不小于 w×h（原始方向）；nil = 只用 Go 解码。
	JPEG func(f *os.File, w, h int) (image.Image, error)
	HEIF bool // 系统能解码 HEIC/HEIF（经 Shell 出图）

	imgSem, vidSem chan struct{}
	mu             sync.Mutex
	calls          map[string]*call
	active         atomic.Int32 // 正在等待生成的真实请求数，预热见到 >0 时让路

	pruneMu  sync.Mutex
	total    atomic.Int64 // 缓存目录估计总字节数；-1 = 未统计
	prefetch struct {
		sync.Mutex
		jobs    []func(context.Context)
		running bool
	}
}

type call struct {
	done chan struct{}
	err  error
}

// New 创建服务并启用 Windows 系统缩略图与 WIC 解码。
func New(dir, ffmpeg string) *Service {
	s := &Service{Dir: dir, FFmpeg: ffmpeg, calls: map[string]*call{},
		imgSem: make(chan struct{}, min(max(runtime.NumCPU(), 2), 4)), vidSem: make(chan struct{}, 2),
		Shell: ShellThumbnail, JPEG: DecodeJPEGScaled, HEIF: HEIFDecoderInstalled()}
	s.total.Store(-1)
	return s
}

// Can 报告文件名对应的缩略图是否可生成（list 的 hasThumb）。
func (s *Service) Can(name string) bool {
	if s == nil {
		return false
	}
	e := ext(name)
	if goExts[e] {
		return true
	}
	if heifExts[e] {
		return s.HEIF && s.Shell != nil
	}
	return s.FFmpeg != "" && media.Kind(name) == media.KindVideo
}

// Source 描述源文件。File 供图片解码（调用方经 fsx 打开）；RealPath 为经 fsx 校验得到的最终真实路径，
// 用于缓存键、系统缩略图与 ffmpeg 输入。
type Source struct {
	Name     string
	RealPath string
	ModTime  time.Time
	Size     int64
	File     io.ReadSeeker
}

func (s *Service) key(src Source, w int) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d\x00%d", cacheVersion, strings.ToLower(src.RealPath), src.ModTime.UnixNano(), src.Size, w))
	return hex.EncodeToString(h[:])
}

// Path 返回缓存文件路径（可能尚未生成）。
func (s *Service) Path(src Source, w int) string {
	return filepath.Join(s.Dir, s.key(src, w)+".jpg")
}

// ETag 返回内容标识（源文件与宽度不变则不变），无需生成即可比较 If-None-Match。
func (s *Service) ETag(src Source, w int) string {
	return `"` + s.key(src, w)[:32] + `"`
}

// Get 返回宽度 w（应已经 WidthFor 取整）的缓存文件路径，必要时生成。
func (s *Service) Get(ctx context.Context, src Source, w int) (string, error) {
	return s.get(ctx, src, w, false)
}

func (s *Service) get(ctx context.Context, src Source, w int, low bool) (string, error) {
	if !s.Can(src.Name) {
		return "", ErrUnsupported
	}
	p := s.Path(src, w)
	if st, err := os.Stat(p); err == nil {
		if time.Since(st.ModTime()) > touchInterval { // 记录访问时间，供淘汰
			now := time.Now()
			os.Chtimes(p, now, now)
		}
		return p, nil
	}
	if !low {
		s.active.Add(1)
		defer s.active.Add(-1)
	}

	s.mu.Lock()
	if c, ok := s.calls[p]; ok { // 同键请求合并
		s.mu.Unlock()
		select {
		case <-c.done:
			return p, c.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	s.calls[p] = c
	s.mu.Unlock()

	c.err = s.generate(ctx, src, w, p)
	s.mu.Lock()
	delete(s.calls, p)
	s.mu.Unlock()
	close(c.done)
	return p, c.err
}

func (s *Service) generate(ctx context.Context, src Source, w int, dst string) error {
	img := goExts[ext(src.Name)] || heifExts[ext(src.Name)]
	sem := s.vidSem
	if img {
		sem = s.imgSem
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if _, err := os.Stat(dst); err == nil { // 排队期间已由他人生成
		return nil
	}
	var data []byte
	var err error
	if img {
		data, err = s.fromImage(src, w)
	} else {
		data, err = s.fromVideo(ctx, src.RealPath, w)
	}
	if err != nil {
		return err
	}
	if err := writeAtomic(s.Dir, dst, data); err != nil {
		return err
	}
	s.added(int64(len(data)))
	return nil
}

func quality(w int) int {
	if w > maxThumbWidth {
		return previewQuality
	}
	return jpegQuality
}

// fit 计算宽度 w 下的输出尺寸：不放大；高度超过 4w 时按 4w 限高。
func fit(sw, sh, w int) (int, int) {
	dw := min(w, sw)
	dh := max(1, sh*dw/sw)
	if dh > 4*w {
		dh = 4 * w
		dw = max(1, sw*dh/sh)
	}
	return dw, dh
}

func (s *Service) fromImage(src Source, w int) ([]byte, error) {
	if !goExts[ext(src.Name)] { // HEIC/HEIF：只能经系统出图，尺寸未知
		return s.fromShellUnknown(src.RealPath, w)
	}
	r := src.File
	if r == nil {
		return nil, ErrUnsupported
	}
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	maxPx := s.MaxPixels
	if maxPx <= 0 {
		maxPx = DefaultMaxPixels
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPx {
		return nil, ErrTooLarge
	}
	orient, exifThumb := 1, []byte(nil)
	jpg := isJPEG(src.Name)
	if jpg {
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		orient, exifThumb = jpegExif(r)
	}
	ow, oh := cfg.Width, cfg.Height // 正向显示尺寸
	if transposed(orient) {
		ow, oh = oh, ow
	}
	dw, dh := fit(ow, oh, w)
	rw, rh := dw, dh // 原始方向下的目标尺寸
	if transposed(orient) {
		rw, rh = dh, dw
	}
	q := quality(w)

	// 1. 内嵌 EXIF 缩略图：仅 160，宽高比与原图一致且不小于目标
	if w == widths[0] && exifThumb != nil {
		if t, err := jpeg.Decode(bytes.NewReader(exifThumb)); err == nil {
			tb := t.Bounds()
			if tb.Dx() >= rw && tb.Dy() >= rh && sameAspect(tb.Dx(), tb.Dy(), cfg.Width, cfg.Height) {
				return encode(applyOrient(scale(t, rw, rh), orient), q)
			}
		}
	}
	// 2. 系统缩略图（已按 EXIF 转正）：JPEG 只取系统缓存（未命中时 WIC 更快），其他格式允许系统生成
	if w <= maxThumbWidth && s.Shell != nil && src.RealPath != "" {
		if t, err := s.Shell(src.RealPath, max(dw, dh), !jpg); err == nil {
			if tb := t.Bounds(); tb.Dx() >= dw-1 && tb.Dy() >= dh-1 { // 系统缓存档位太小则不用
				return encode(scale(t, dw, dh), q)
			}
		}
	}
	// 3. WIC 按比例解码 JPEG
	if f, ok := r.(*os.File); ok && jpg && s.JPEG != nil {
		if t, err := s.JPEG(f, rw, rh); err == nil {
			return encode(applyOrient(scale(t, rw, rh), orient), q)
		}
	}
	// 4. Go 解码
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	t, _, err := image.Decode(r)
	if err != nil {
		return nil, err
	}
	return encode(applyOrient(scale(t, rw, rh), orient), q)
}

// fromShellUnknown 在源尺寸未知时经系统出图：先按 w×w 框取，竖图宽度不足再按比例放大框重取一次。
func (s *Service) fromShellUnknown(path string, w int) ([]byte, error) {
	if s.Shell == nil || path == "" {
		return nil, ErrUnsupported
	}
	t, err := s.Shell(path, w, true)
	if err != nil {
		return nil, err
	}
	if b := t.Bounds(); b.Dy() > b.Dx() && b.Dx() < w && max(b.Dx(), b.Dy()) >= w-1 {
		if t2, err := s.Shell(path, min(4*w, (w*b.Dy()+b.Dx()-1)/b.Dx()), true); err == nil {
			t = t2
		}
	}
	b := t.Bounds()
	dw, dh := fit(b.Dx(), b.Dy(), w)
	return encode(scale(t, dw, dh), quality(w))
}

func sameAspect(w1, h1, w2, h2 int) bool {
	d := w1*h2 - w2*h1
	if d < 0 {
		d = -d
	}
	return d*50 <= w2*h1 // 相差不超过 2%
}

// scale 缩放到 dw×dh，透明处铺白底；尺寸相同且已是 RGBA 时直接返回。
func scale(img image.Image, dw, dh int) *image.RGBA {
	sb := img.Bounds()
	if rgba, ok := img.(*image.RGBA); ok && sb.Dx() == dw && sb.Dy() == dh && sb.Min == (image.Point{}) {
		return rgba
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	op := draw.Src
	if o, ok := img.(interface{ Opaque() bool }); !ok || !o.Opaque() {
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		op = draw.Over
	}
	var k draw.Scaler = draw.CatmullRom
	if sb.Dx() <= 2*dw && sb.Dy() <= 2*dh { // 缩小不足一半时近似双线性已足够，比精确核快一个数量级
		k = draw.ApproxBiLinear
	}
	k.Scale(dst, dst.Bounds(), img, sb, op, nil)
	return dst
}

func encode(img image.Image, q int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ffmpegArgs 构造截帧参数：路径作为独立参数（加 file: 前缀，避免被当作协议或选项），不经 shell。
func ffmpegArgs(path string, seek, w int) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-ss", fmt.Sprint(seek), "-i", "file:" + path,
		"-frames:v", "1", "-vf", fmt.Sprintf("scale=min(%d\\,iw):-2", w),
		"-f", "image2pipe", "-c:v", "mjpeg", "-q:v", "4", "pipe:1",
	}
}

// fromVideo 截取第 10 秒一帧（片长不足 10 秒时取第 0 秒）。
func (s *Service) fromVideo(ctx context.Context, path string, w int) ([]byte, error) {
	if s.FFmpeg == "" {
		return nil, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ffmpegTimeout)
	defer cancel()
	var last error
	for _, seek := range []int{10, 0} {
		cmd := exec.CommandContext(ctx, s.FFmpeg, ffmpegArgs(path, seek, w)...)
		var out limitedBuffer
		out.max = maxFFmpegOutput
		cmd.Stdout = &out
		err := cmd.Run()
		if err == nil && out.Len() > 0 {
			return out.Bytes(), nil
		}
		last = cmp.Or(err, errors.New("ffmpeg 未输出画面"))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("ffmpeg 输出过大")
	}
	return b.Buffer.Write(p)
}

func writeAtomic(dir, dst string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // rename 成功后为空操作
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		if _, serr := os.Stat(dst); serr == nil {
			return nil // 已存在（如被并发读取占用无法替换），内容相同
		}
		return err
	}
	return nil
}

// added 记录新写入的字节数；首次写入时统计目录，超过上限即同步淘汰。
func (s *Service) added(n int64) {
	if s.total.Load() < 0 || s.total.Add(n) > cmp.Or(s.MaxBytes, DefaultMaxBytes) {
		s.Prune()
	}
}

// Prune 统计缓存目录并淘汰：总量超过上限时按 mtime（最近访问时间）从旧到新删除，直到不超过上限的 90%；
// 同时清理 1 小时前遗留的临时文件。
func (s *Service) Prune() {
	s.pruneMu.Lock()
	defer s.pruneMu.Unlock()
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	for _, e := range ents {
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		p := filepath.Join(s.Dir, e.Name())
		if strings.HasPrefix(e.Name(), "tmp-") {
			if time.Since(fi.ModTime()) > time.Hour {
				os.Remove(p)
			}
			continue
		}
		files = append(files, file{p, fi.Size(), fi.ModTime()})
		total += fi.Size()
	}
	limit := cmp.Or(s.MaxBytes, DefaultMaxBytes)
	if total > limit {
		slices.SortFunc(files, func(a, b file) int { return a.mod.Compare(b.mod) })
		for _, f := range files {
			if total <= limit*9/10 {
				break
			}
			if os.Remove(f.path) == nil {
				total -= f.size
			}
		}
	}
	s.total.Store(total)
}

// Prefetch 以 jobs 替换后台预热队列（最多 MaxPrefetch 个；新目录优先，旧队列剩余项丢弃），立即返回。
// 队列由单个后台 goroutine 依次执行；有真实请求在等待生成时先让路。
func (s *Service) Prefetch(jobs []func(context.Context)) {
	p := &s.prefetch
	p.Lock()
	defer p.Unlock()
	p.jobs = slices.Clone(jobs[:min(len(jobs), MaxPrefetch)])
	if p.running || len(p.jobs) == 0 {
		return
	}
	p.running = true
	go s.prefetchLoop()
}

func (s *Service) prefetchLoop() {
	p := &s.prefetch
	for {
		for s.active.Load() > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		p.Lock()
		if len(p.jobs) == 0 {
			p.running = false
			p.Unlock()
			return
		}
		job := p.jobs[0]
		p.jobs = p.jobs[1:]
		p.Unlock()
		job(context.Background())
	}
}

// Warm 供预热任务调用：同 Get，但以低优先级生成（不计入真实请求）。
func (s *Service) Warm(ctx context.Context, src Source, w int) error {
	_, err := s.get(ctx, src, w, true)
	return err
}

// WaitIdle 等待预热队列执行完毕（测试用）。
func (s *Service) WaitIdle() {
	for {
		s.prefetch.Lock()
		running := s.prefetch.running
		s.prefetch.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
