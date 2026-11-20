package api

import (
	"cmp"
	"net/http"
	"path"
	"regexp"
	"strings"

	"lfb/server/internal/episode"
	"lfb/server/internal/listing"
	"lfb/server/internal/media"
)

// 下一集与字幕（03 文档 4.6、4.7）。集号解析用 internal/episode（与整理库共用）。

// GET /api/next?share=&path=
// 返回 {item}（同 list 条目，另含 path）或 {item: null}。跨季（P3）未实现。
func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	dir, name, items, ok := s.siblings(w, r)
	if !ok {
		return
	}
	next := nextVideo(dir, name, items)
	if next == nil {
		writeJSON(w, map[string]any{"item": nil})
		return
	}
	writeJSON(w, map[string]any{"item": mediaItem{*next, path.Join(dir, next.Name)}})
}

// nextVideo 在同目录条目 items（已自然排序、不含当前文件）中找 name 的下一集（03 文档 4.6）：
// 当前文件有集号时取集号更大的最小者，能解析出剧名时只看同一部剧（规范化剧名忽略大小写相同）；
// 当前文件无集号，或其他视频都解析不出集号时，取自然排序的下一个视频。无则返回 nil。
func nextVideo(dir, name string, items []listItem) *listItem {
	cur, curOK := episode.ParseWithDir(dir, name)
	var next, natural *listItem
	var nextInfo episode.Info
	anyEpisode := false
	for i := range items {
		it := &items[i]
		if it.Kind != media.KindVideo {
			continue
		}
		if natural == nil && listing.NaturalLess(name, it.Name) {
			natural = it
		}
		e, ok := episode.ParseWithDir(dir, it.Name)
		anyEpisode = anyEpisode || ok
		if curOK && ok && (cur.Show == "" || strings.EqualFold(e.Show, cur.Show)) && epCompare(e, cur) > 0 &&
			(next == nil || epCompare(e, nextInfo) < 0) {
			next, nextInfo = it, e
		}
	}
	if !curOK || !anyEpisode {
		return natural
	}
	return next
}

type mediaItem struct {
	listItem
	Path string `json:"path"`
}

// epCompare 按（季, 集）比较；未标季视为第 1 季。
func epCompare(a, b episode.Info) int {
	return cmp.Or(cmp.Compare(max(a.Season, 1), max(b.Season, 1)), cmp.Compare(a.Episode, b.Episode))
}

type subtitleItem struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Format string `json:"format"`
	Lang   string `json:"lang"`
	Match  string `json:"match"`
}

var reLangTag = regexp.MustCompile(`^[A-Za-z]{2,3}(?:[-_][A-Za-z0-9]{2,4})?$`)

// GET /api/subtitles?share=&path=
// exact：同名或同名+语言后缀；episode：同目录字幕集号与视频相同（季号缺省时不比较）。
func (s *Server) handleSubtitles(w http.ResponseWriter, r *http.Request) {
	dir, name, items, ok := s.siblings(w, r)
	if !ok {
		return
	}
	stem := strings.TrimSuffix(name, path.Ext(name))
	cur, curOK := episode.ParseWithDir(dir, name)
	exact, byEpisode := []subtitleItem{}, []subtitleItem{}
	for _, it := range items {
		if it.Kind != media.KindSubtitle {
			continue
		}
		ext := path.Ext(it.Name)
		sstem := strings.TrimSuffix(it.Name, ext)
		sub := subtitleItem{Name: it.Name, Path: path.Join(dir, it.Name), Format: strings.ToLower(ext[1:])}
		switch {
		case strings.EqualFold(sstem, stem):
			sub.Match = "exact"
		case len(sstem) > len(stem)+1 && sstem[len(stem)] == '.' && strings.EqualFold(sstem[:len(stem)], stem):
			sub.Match, sub.Lang = "exact", sstem[len(stem)+1:]
		default:
			e, ok := episode.ParseWithDir(dir, it.Name)
			if !curOK || !ok || e.Episode != cur.Episode || e.Season != 0 && cur.Season != 0 && e.Season != cur.Season {
				continue
			}
			sub.Match = "episode"
			if i := strings.LastIndexByte(sstem, '.'); i >= 0 && reLangTag.MatchString(sstem[i+1:]) {
				sub.Lang = sstem[i+1:]
			}
			byEpisode = append(byEpisode, sub)
			continue
		}
		exact = append(exact, sub)
	}
	writeJSON(w, map[string]any{"items": append(exact, byEpisode...)})
}

// siblings 校验当前文件（共享可见、路径合法、存在且对本设备可见、不是目录），返回其所在目录
// （共享内相对路径，/ 分隔）、文件名，以及该目录中对本设备可见的文件条目（不含当前文件，
// 已按名称自然排序）。出错时已写入响应并返回 ok=false。
func (s *Server) siblings(w http.ResponseWriter, r *http.Request) (dir, name string, items []listItem, ok bool) {
	sh := s.visibleShare(r)
	if sh == nil {
		writeError(w, CodeNotFound, "共享不存在")
		return
	}
	rel := strings.ReplaceAll(r.URL.Query().Get("path"), `\`, "/")
	f, fi, err := openInShare(sh, rel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	f.Close()
	if fi.IsDir() {
		writeError(w, CodeNotFound, "文件不存在")
		return
	}
	dir, name = path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	d, _, err := openInShare(sh, dir)
	if err != nil {
		writeFSError(w, err)
		return
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		writeError(w, CodeNotFound, "目录无法读取")
		return
	}
	for _, e := range entries {
		if it, ok := entryItem(sh, dir, e); ok && it.Kind != media.KindDir && !strings.EqualFold(it.Name, name) {
			items = append(items, it)
		}
	}
	sortItems(items, "name", false)
	return dir, name, items, true
}
