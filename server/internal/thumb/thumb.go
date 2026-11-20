// Package thumb 生成并缓存缩略图（03 文档 4.5）。
//
// 图片（jpg/png/gif/webp）用 Go 图像库解码缩放；视频仅在找到 ffmpeg 时截取一帧。输出一律 JPEG，
// 缓存在磁盘目录中，键为 sha256(真实路径 + mtime + size + 宽度)，文件变更后键自然失效。
// 生成并发上限 2，同键并发请求合并为一次生成。调用方负责路径安全与可见性校验。
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
	"strings"
	"sync"
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
	ffmpegTimeout    = 20 * time.Second
	maxFFmpegOutput  = 8 << 20
	jpegQuality      = 80
)

var widths = []int{160, 320, 640}

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}

// Width 把请求宽度取整到 160/320/640（向上取整，超过 640 取 640）。
func Width(w int) int {
	for _, x := range widths {
		if w <= x {
			return x
		}
	}
	return widths[len(widths)-1]
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

	sem   chan struct{}
	mu    sync.Mutex
	calls map[string]*call
}

type call struct {
	done chan struct{}
	err  error
}

func New(dir, ffmpeg string) *Service {
	return &Service{Dir: dir, FFmpeg: ffmpeg, sem: make(chan struct{}, 2), calls: map[string]*call{}}
}

// Can 报告文件名对应的缩略图是否可生成（list 的 hasThumb）。
func (s *Service) Can(name string) bool {
	if s == nil {
		return false
	}
	if imageExts[strings.ToLower(filepath.Ext(name))] {
		return true
	}
	return s.FFmpeg != "" && media.Kind(name) == media.KindVideo
}

// Source 描述源文件。File 供图片解码（调用方经 fsx 打开）；RealPath 用于缓存键与 ffmpeg 输入。
type Source struct {
	Name     string
	RealPath string
	ModTime  time.Time
	Size     int64
	File     io.ReadSeeker
}

// Path 返回缓存文件路径（可能尚未生成）。
func (s *Service) Path(src Source, w int) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d\x00%d", strings.ToLower(src.RealPath), src.ModTime.UnixNano(), src.Size, w))
	return filepath.Join(s.Dir, hex.EncodeToString(h[:])+".jpg")
}

// Get 返回宽度 w（应已经 Width 取整）的缩略图缓存文件路径，必要时生成。
func (s *Service) Get(ctx context.Context, src Source, w int) (string, error) {
	if !s.Can(src.Name) {
		return "", ErrUnsupported
	}
	p := s.Path(src, w)
	if _, err := os.Stat(p); err == nil {
		return p, nil
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
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if _, err := os.Stat(dst); err == nil { // 排队期间已由他人生成
		return nil
	}
	var data []byte
	var err error
	if imageExts[strings.ToLower(filepath.Ext(src.Name))] {
		data, err = s.fromImage(src.File, w)
	} else {
		data, err = s.fromVideo(ctx, src.RealPath, w)
	}
	if err != nil {
		return err
	}
	return writeAtomic(s.Dir, dst, data)
}

func (s *Service) fromImage(r io.ReadSeeker, w int) ([]byte, error) {
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
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, err
	}
	return encode(img, w)
}

// encode 缩放到宽度 w（不放大；高度过大时按 4w 限高），透明处铺白底，输出 JPEG。
func encode(img image.Image, w int) ([]byte, error) {
	sb := img.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dw := min(w, sw)
	dh := max(1, sh*dw/sw)
	if dh > 4*w {
		dh = 4 * w
		dw = max(1, sw*dh/sh)
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, sb, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
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
