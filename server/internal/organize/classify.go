package organize

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"lfb/server/internal/config"
	"lfb/server/internal/episode"
	"lfb/server/internal/fsx"
)

// 类别（06 文档第 6 节 category）。
const (
	CatPhoto    = "photo"
	CatEpisode  = "episode"
	CatMovie    = "movie"
	CatUnsorted = "unsorted"
	CatKept     = "kept" // 保持源文件夹结构，不归类
)

// 文件种类（kind）。
const (
	KindVideo    = "video"
	KindImage    = "image"
	KindSubtitle = "subtitle"
)

var kinds = map[string]string{}

func init() {
	for _, e := range strings.Fields("mp4 m4v mkv webm avi mov wmv flv ts m2ts mts rmvb 3gp") {
		kinds[e] = KindVideo
	}
	for _, e := range strings.Fields("jpg jpeg png gif webp heic heif bmp") {
		kinds[e] = KindImage
	}
	for _, e := range strings.Fields("srt ass ssa vtt") {
		kinds[e] = KindSubtitle
	}
}

// kindOf 返回整理关心的文件种类（06 文档 4.1），其他文件返回 ""。
func kindOf(name string) string {
	return kinds[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
}

// tempExts 为下载中的临时文件扩展名（06 文档 4.2 #1）。
var tempExts = []string{".part", ".!qb", ".td", ".downloading", ".crdownload", ".aria2", ".tmp"}

func isTempName(name string) bool {
	l := strings.ToLower(name)
	for _, e := range tempExts {
		if strings.HasSuffix(l, e) {
			return true
		}
	}
	return false
}

// Plan 为归类结果：库目录内的目标目录（/ 分隔）与文件名（原文件名，重名时复制阶段再加 (1)）。
type Plan struct {
	Category string
	Dir      string
	Name     string
}

// Target 返回库目录内的相对路径。
func (p Plan) Target() string { return path.Join(p.Dir, p.Name) }

var (
	reCamera   = regexp.MustCompile(`(?i)^(?:VID|MVIMG|PXL)_|^VID-\d{8}-WA`)
	reCameraIM = regexp.MustCompile(`(?i)^IMG_`)
	reDate     = regexp.MustCompile(`(?:^|\D)((?:19|20)\d{2})[-_.]?(\d{2})[-_.]?(\d{2})(?:\D|$)`)
	reYear     = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})(?:[^0-9]|$)`)
)

// classify 按 06 文档 5.1 的规则顺序归类。rel 为相对源文件夹的路径（/ 分隔）；
// photoTime 返回图片的 EXIF 拍摄时间（可为 nil）；无 EXIF 时取文件名中的日期，再无则取修改时间。
func classify(rel string, mtime time.Time, photoTime func() (time.Time, bool)) Plan {
	dir, name := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	kind := kindOf(name)
	stem := strings.TrimSuffix(name, path.Ext(name))
	photoDir := func(t time.Time) Plan {
		return Plan{CatPhoto, fmt.Sprintf("照片/%04d/%02d", t.Year(), int(t.Month())), name}
	}

	if kind == KindImage { // 1 照片
		if photoTime != nil {
			if t, ok := photoTime(); ok {
				return photoDir(t)
			}
		}
		if t, ok := nameDate(stem); ok {
			return photoDir(t)
		}
		return photoDir(mtime)
	}
	if kind != KindVideo {
		return Plan{CatUnsorted, "未分类", name}
	}
	ext := strings.ToLower(path.Ext(name))
	if reCamera.MatchString(name) || reCameraIM.MatchString(name) && (ext == ".mov" || ext == ".mp4") { // 2 手机短视频
		if t, ok := nameDate(stem); ok {
			return photoDir(t)
		}
		return photoDir(mtime)
	}
	if info, ok := episode.ParseWithDir(dir, name); ok { // 3 剧集
		show := safeDirName(info.Show)
		if show == "" {
			return Plan{CatUnsorted, "未分类", name}
		}
		season := info.Season
		if season == 0 && dir != "" {
			season = episode.SeasonOf(path.Base(dir))
		}
		return Plan{CatEpisode, fmt.Sprintf("剧集/%s/第%d季", show, max(season, 1)), name}
	}
	// 4 电影：识别出年份时 电影/<名称> (<年份>)/
	for _, m := range reYear.FindAllStringSubmatchIndex(stem, -1) {
		if title := safeDirName(episode.Normalize(stem[:m[2]])); title != "" {
			return Plan{CatMovie, "电影/" + safeDirName(title+" ("+stem[m[2]:m[3]]+")"), name}
		}
	}
	if episode.Normalize(stem) != "" {
		return Plan{CatMovie, "电影", name}
	}
	return Plan{CatUnsorted, "未分类", name} // 5
}

// keepPlan 保持源文件夹结构：目标即相对源文件夹的路径，各段须通过 fsx 词法校验，否则返回 false（不整理）。
func keepPlan(rel string) (Plan, bool) {
	if rel == "" || !fsx.ValidRel(rel) {
		return Plan{}, false
	}
	dir, name := path.Split(rel)
	return Plan{CatKept, strings.TrimSuffix(dir, "/"), name}, true
}

// planFor 按源的设置计算目标：保持结构或按第 5 节归类。
func planFor(src config.OrganizeSource, e entry, photoTime func() (time.Time, bool)) (Plan, bool) {
	if src.KeepStructure {
		return keepPlan(e.Rel)
	}
	return classify(e.Rel, e.Mtime, photoTime), true
}

// nameDate 取文件名中前后不接数字的合法日期：YYYYMMDD 或以 - _ . 分隔的 YYYY-MM-DD 等
// （IMG_20250314_101010、PXL_20250314_…、Screenshot_2025-03-14…）。
func nameDate(stem string) (time.Time, bool) {
	for _, m := range reDate.FindAllStringSubmatch(stem, -1) {
		if t, err := time.ParseInLocation("20060102", m[1]+m[2]+m[3], time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

var deviceNames = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}

func init() {
	for _, c := range "0123456789¹²³" {
		deviceNames["COM"+string(c)] = true
		deviceNames["LPT"+string(c)] = true
	}
}

// safeDirName 按 03 §2.2 清洗目录名：非法字符替换为空格、去结尾点与空格、设备名加后缀 _、截断到 255 字符。
func safeDirName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 255 {
		s = string([]rune(s)[:255])
	}
	s = strings.TrimRight(s, ". ")
	s = strings.TrimLeft(s, " ")
	if s == "" {
		return ""
	}
	base, _, _ := strings.Cut(s, ".")
	if deviceNames[strings.ToUpper(strings.TrimRight(base, " "))] {
		s = base + "_" + s[len(base):]
	}
	return s
}
