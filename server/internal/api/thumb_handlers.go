package api

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"lfb/server/internal/config"
	"lfb/server/internal/fsx"
	"lfb/server/internal/media"
	"lfb/server/internal/thumb"
)

// 缩略图与预览图（03 文档 4.5）。已在 auth.Service 的 ?t= 白名单中。

const defaultThumbWidth = 320

// GET /api/thumb?share=&path=&w=&ver=
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	width := defaultThumbWidth
	if v := q.Get("w"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, CodeBadPath, "参数非法")
			return
		}
		width = n
	}

	sh := s.visibleShare(r)
	if sh == nil {
		writeError(w, CodeNotFound, "共享不存在")
		return
	}
	rel := q.Get("path")
	f, fi, err := openInShare(sh, rel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	defer f.Close()
	name := filepath.Base(filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/"))) // 用请求名判定类型
	if fi.IsDir() || !s.Thumbs.Can(name) {
		writeError(w, CodeNotFound, "无缩略图")
		return
	}
	src, err := thumbSource(f, fi, name)
	if err != nil {
		writeError(w, CodeNotFound, "无缩略图")
		return
	}
	width = thumb.WidthFor(name, width)

	// ver=<mtime>_<size>（取自 list 条目）与当前文件一致时 URL 唯一对应内容，可强缓存
	etag := s.Thumbs.ETag(src, width)
	cc := "private, max-age=86400"
	if v := q.Get("ver"); v == strconv.FormatInt(fi.ModTime().UnixMilli(), 10)+"_"+strconv.FormatInt(fi.Size(), 10) {
		cc = "private, max-age=31536000, immutable"
	} else if v != "" {
		cc = "private, no-cache"
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cc)
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	p, err := s.Thumbs.Get(r.Context(), src, width)
	if err != nil {
		writeError(w, CodeNotFound, "无缩略图")
		return
	}
	cf, err := os.Open(p) // 缓存目录由服务端生成，不在共享内
	if err != nil {
		writeError(w, CodeNotFound, "无缩略图")
		return
	}
	defer cf.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, "", fi.ModTime(), cf)
}

// thumbSource 用经 fsx 打开的 f 构造缩略图源：RealPath 取句柄的最终真实路径（不含链接与短名），
// 系统缩略图与 ffmpeg 只收这个路径。
func thumbSource(f *os.File, fi fs.FileInfo, name string) (thumb.Source, error) {
	real, err := fsx.FinalPath(f)
	if err != nil {
		return thumb.Source{}, err
	}
	return thumb.Source{Name: name, RealPath: real, ModTime: fi.ModTime(), Size: fi.Size(), File: f}, nil
}

// hasThumb 供 /api/list：常见图片恒为 true，HEIC 仅在系统能解码时为 true，视频仅在 ffmpeg 可用时为 true。
func (s *Server) hasThumb(name string) bool { return s.Thumbs.Can(name) }

// prefetchThumbs 在后台为列表中的图片（从当前页起，最多 thumb.MaxPrefetch 个）预生成 320 缩略图；立即返回。
// 文件在执行时重新经 fsx 与隐藏规则打开。
func (s *Server) prefetchThumbs(sh *config.Share, dir string, items []listItem) {
	if s.Thumbs == nil {
		return
	}
	dir = strings.ReplaceAll(dir, `\`, "/")
	var jobs []func(context.Context)
	for _, it := range items {
		if len(jobs) == thumb.MaxPrefetch {
			break
		}
		if it.Kind != media.KindImage || !it.HasThumb {
			continue
		}
		rel := path.Join(dir, it.Name)
		jobs = append(jobs, func(ctx context.Context) {
			f, fi, err := openInShare(sh, rel)
			if err != nil {
				return
			}
			defer f.Close()
			if src, err := thumbSource(f, fi, it.Name); err == nil && !fi.IsDir() {
				s.Thumbs.Warm(ctx, src, thumb.PrefetchWidth)
			}
		})
	}
	s.Thumbs.Prefetch(jobs)
}
