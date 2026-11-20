package api

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"lfb/server/internal/activity"
	"lfb/server/internal/config"
	"lfb/server/internal/fsx"
	"lfb/server/internal/listing"
	"lfb/server/internal/media"
)

// 以下接口均已经过 requireDevice，设备 ID 用 DeviceID(r.Context()) 获取。

const (
	defaultListLimit = 200
	maxListLimit     = 1000
	streamIdle       = 30 * time.Second // 流式响应「无进展超时」（05 文档 3.3）
)

// GET /api/shares
func (s *Server) handleShares(w http.ResponseWriter, r *http.Request) {
	type perms struct {
		Download   bool `json:"download"`
		Upload     bool `json:"upload"`
		Modify     bool `json:"modify"`
		ShowHidden bool `json:"showHidden"`
	}
	type share struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Perms  perms  `json:"perms"`
		Status string `json:"status"`
	}
	dev := DeviceID(r.Context())
	items := []share{}
	for _, sh := range s.allShares() {
		if !visible(&sh, dev) {
			continue
		}
		p := sh.Perms
		items = append(items, share{sh.ID, sh.Name, perms{p.Download, p.Upload, p.Delete, p.Hidden}, shareStatus(sh.Path)})
	}
	writeJSON(w, map[string]any{"items": items})
}

type listItem struct {
	Name     string        `json:"name"`
	Kind     string        `json:"kind"`
	Size     int64         `json:"size"`
	MTime    int64         `json:"mtime"`
	Hidden   bool          `json:"hidden"`
	HasThumb bool          `json:"hasThumb"`
	Progress *itemProgress `json:"progress"` // 仅视频，本设备进度；无则 null
}

// GET /api/list?share=&path=&sort=&order=&offset=&limit=
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sortBy, order := cmp.Or(q.Get("sort"), "name"), cmp.Or(q.Get("order"), "asc")
	offset, err1 := intParam(q.Get("offset"), 0)
	limit, err2 := intParam(q.Get("limit"), defaultListLimit)
	if err1 != nil || err2 != nil || offset < 0 || limit < 1 ||
		!slices.Contains([]string{"name", "mtime", "size"}, sortBy) || !slices.Contains([]string{"asc", "desc"}, order) {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	limit = min(limit, maxListLimit)

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
	if !fi.IsDir() {
		writeError(w, CodeNotFound, "不是目录")
		return
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		writeError(w, CodeNotFound, "目录无法读取")
		return
	}

	items := make([]listItem, 0, len(entries))
	for _, e := range entries {
		if it, ok := entryItem(sh, rel, e); ok {
			it.HasThumb = it.Kind != media.KindDir && s.hasThumb(it.Name)
			items = append(items, it)
		}
	}
	s.fillProgress(r.Context(), DeviceID(r.Context()), sh.ID, rel, items)
	sortItems(items, sortBy, order == "desc")

	etag := listETag(items, sh.Perms, sortBy, order, offset, limit)
	w.Header().Set("ETag", etag)
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	total := len(items)
	start := min(offset, total)
	page := items[start:min(start+limit, total)]
	writeJSON(w, map[string]any{"items": page, "total": total})
}

// entryItem 把目录项转换为列表条目；始终隐藏项、无权查看的隐藏项、逃出共享的链接返回 false。
func entryItem(sh *config.Share, dirRel string, e fs.DirEntry) (listItem, bool) {
	name := e.Name()
	if fsx.IsAlwaysHidden(name) {
		return listItem{}, false
	}
	fi, err := e.Info()
	if err != nil {
		return listItem{}, false
	}
	hidden := fsx.IsHidden(fi)
	if hidden && !sh.Perms.Hidden {
		return listItem{}, false
	}
	if e.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		// 链接/联接：仅目标仍在共享内才列出（03 文档 2.3），属性取目标的；
		// 目标或其真实路径上的祖先隐藏时，链接也按隐藏处理
		lf, final, err := fsx.OpenFinal(sh.Path, path.Join(strings.ReplaceAll(dirRel, `\`, "/"), name))
		if err != nil {
			return listItem{}, false
		}
		fi, err = lf.Stat()
		lf.Close()
		if err != nil {
			return listItem{}, false
		}
		if finalHidden(sh.Path, final, fi) {
			if !sh.Perms.Hidden {
				return listItem{}, false
			}
			hidden = true
		}
	}
	it := listItem{Name: name, MTime: fi.ModTime().UnixMilli(), Hidden: hidden}
	if fi.IsDir() {
		it.Kind = media.KindDir
	} else {
		it.Kind, it.Size = media.Kind(name), fi.Size()
	}
	return it, true
}

// sortItems：目录始终在前；同类按 sortBy 排序，desc 反转；mtime/size 相同时按名称自然排序。
func sortItems(items []listItem, sortBy string, desc bool) {
	slices.SortStableFunc(items, func(a, b listItem) int {
		if ad, bd := a.Kind == media.KindDir, b.Kind == media.KindDir; ad != bd {
			if ad {
				return -1
			}
			return 1
		}
		c := 0
		switch sortBy {
		case "mtime":
			c = cmp.Compare(a.MTime, b.MTime)
		case "size":
			c = cmp.Compare(a.Size, b.Size)
		}
		if c == 0 {
			switch {
			case listing.NaturalLess(a.Name, b.Name):
				c = -1
			case listing.NaturalLess(b.Name, a.Name):
				c = 1
			}
		}
		if desc {
			c = -c
		}
		return c
	})
}

// listETag 哈希过滤排序后的完整列表（名称+大小+mtime+隐藏+hasThumb+本设备进度）与权限位；
// 同时计入排序与分页参数，使不同查询的响应不共用同一 ETag。
func listETag(items []listItem, p config.Perms, sortBy, order string, offset, limit int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%t|%t|%t|%t|%s|%s|%d|%d\n", p.Download, p.Upload, p.Delete, p.Hidden, sortBy, order, offset, limit)
	for _, it := range items {
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d\x00%t\x00%t", it.Name, it.Kind, it.Size, it.MTime, it.Hidden, it.HasThumb)
		if p := it.Progress; p != nil {
			fmt.Fprintf(h, "\x00%g\x00%g\x00%t", p.Position, p.Duration, p.Watched)
		}
		h.Write([]byte{'\n'})
	}
	return `"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
}

func etagMatch(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == etag {
			return true
		}
	}
	return false
}

// GET /api/file?share=&path=（允许 ?t=）
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) { s.serveFile(w, r, false) }

// GET /api/download?share=&path=（需 download 权限）
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) { s.serveFile(w, r, true) }

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, download bool) {
	sh := s.visibleShare(r)
	if sh == nil {
		writeError(w, CodeNotFound, "共享不存在")
		return
	}
	rel := r.URL.Query().Get("path")
	f, fi, err := openInShare(sh, rel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	defer f.Close()
	if download && !sh.Perms.Download { // 顺序同 03 文档第 3 节：在线 → 路径 → 操作权限
		writeError(w, CodeForbidden, "该共享不允许下载")
		return
	}
	if fi.IsDir() {
		writeError(w, CodeNotFound, "文件不存在")
		return
	}

	name := path.Base(strings.ReplaceAll(rel, `\`, "/")) // 用请求名而非链接目标名
	w.Header().Set("Content-Type", media.MIME(name))     // 不压缩（05 文档 3.2）
	if download {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+rfc5987(name))
	}
	// 包装读取器每读一块续期写期限（05 文档 3.3）。会使 sendfile 失效，但桌面 Windows 本就不走
	// TransmitFile；ServeContent 仍经 response.ReadFrom 的缓冲拷贝，不额外包 writer。
	rc := http.NewResponseController(w)
	if !download && s.Activity != nil && media.Kind(name) == media.KindVideo { // 活跃播放（05 文档 3.4）
		defer s.Activity.StreamStart(activity.VolumeOf(filepath.Join(sh.Path, rel)))()
	}
	http.ServeContent(w, r, name, fi.ModTime(), deadlineReader{f, rc})
}

type deadlineReader struct {
	io.ReadSeeker
	rc *http.ResponseController
}

func (d deadlineReader) Read(p []byte) (int, error) {
	_ = d.rc.SetWriteDeadline(time.Now().Add(streamIdle)) // 无进展 30s 即断；不支持时忽略
	return d.ReadSeeker.Read(p)
}

// rfc5987 按 RFC 5987 attr-char 编码文件名（UTF-8 字节，非 attr-char 一律 %XX）。
func rfc5987(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// openInShare 经 fsx.OpenFinal 安全打开 rel；无「显示隐藏文件」权限时，请求路径任一段以 . 开头、
// 或解析链接后的真实路径任一段为隐藏项，即视为不存在（03 文档 2.4）。
func openInShare(sh *config.Share, rel string) (*os.File, fs.FileInfo, error) {
	f, final, err := fsx.OpenFinal(sh.Path, rel)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err == nil && !sh.Perms.Hidden {
		err = checkNotHidden(sh.Path, rel, final, fi)
	}
	if err != nil {
		f.Close()
		return nil, nil, fsx.ErrNotFound
	}
	return f, fi, nil
}

func checkNotHidden(shareRoot, rel string, final []string, fi fs.FileInfo) error {
	if rel == "" {
		return nil
	}
	for _, s := range strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/") {
		if strings.HasPrefix(s, ".") {
			return fsx.ErrNotFound
		}
	}
	if finalHidden(shareRoot, final, fi) {
		return fsx.ErrNotFound
	}
	return nil
}

// finalHidden 报告真实最终路径 final（fsx.OpenFinal 返回，不含链接）上是否有隐藏项：
// 任一段以 . 开头，或最终项 fi / 祖先目录带 Hidden 属性。祖先无法打开时保守视为隐藏。
func finalHidden(shareRoot string, final []string, fi fs.FileInfo) bool {
	if len(final) == 0 {
		return false // 共享根
	}
	for _, s := range final {
		if strings.HasPrefix(s, ".") {
			return true
		}
	}
	if fsx.IsHidden(fi) {
		return true
	}
	for i := 1; i < len(final); i++ {
		pf, err := fsx.Open(shareRoot, strings.Join(final[:i], "/"))
		if err != nil {
			return true
		}
		pfi, err := pf.Stat()
		pf.Close()
		if err != nil || fsx.IsHidden(pfi) {
			return true
		}
	}
	return false
}

// visibleShare 返回对当前设备可见的共享；不存在或不可见均返回 nil（统一 404，不泄露存在性）。
func (s *Server) visibleShare(r *http.Request) *config.Share {
	id := r.URL.Query().Get("share")
	for _, sh := range s.allShares() {
		if sh.ID == id && visible(&sh, DeviceID(r.Context())) {
			return &sh
		}
	}
	return nil
}

// allShares 返回当前配置快照中的共享（只读）；每个请求只取一次快照。
func (s *Server) allShares() []config.Share {
	if s.Config == nil {
		return nil
	}
	return s.Config.Get().Shares
}

// shareStatus 返回 ok，或路径不存在/不是目录时的 offline。
func shareStatus(p string) string {
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return "ok"
	}
	return "offline"
}

func visible(sh *config.Share, dev string) bool {
	return len(sh.Devices) == 0 || slices.Contains(sh.Devices, dev)
}

func writeFSError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fsx.ErrBadPath):
		writeError(w, CodeBadPath, "路径非法")
	case errors.Is(err, fsx.ErrShareOffline):
		writeError(w, CodeShareOffline, "共享文件夹不可用（硬盘未连接？）")
	default:
		writeError(w, CodeNotFound, "文件不存在")
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func intParam(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}
