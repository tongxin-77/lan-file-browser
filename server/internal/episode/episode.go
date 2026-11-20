// Package episode 从文件名解析剧名、季、集号，供“下一集”（03 文档 4.6）与自动整理（06 文档 5.2）共用。
// 排除 1080p、x265、年份等干扰数字的规则只在此维护一份。
package episode

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"lfb/server/internal/media"
)

// Info 为解析结果。
type Info struct {
	Show    string // 规范化后的剧名；文件名中没有剧名（如 `12.mkv`、`第12集.mp4`）时为空，由调用方取父文件夹名
	Season  int    // 季号；文件名中未出现时为 0（调用方按默认 1 处理）
	Episode int    // 集号
}

const cnDigits = `零〇一二两三四五六七八九十百`

var cnDigit = map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}

var (
	// 带边界的模式用第 1 组标出记号本身，剧名取记号之前（边界字符可能是剧名的最后一个字）
	reSE      = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(s(\d{1,3})[ ._-]?e(\d{1,4}))(?:\D|$)`)
	reEP      = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])((?:ep?|episode)[ ._]?(\d{1,4}))(?:\D|$)`)
	reDi      = regexp.MustCompile(`第\s*([0-9]{1,4}|[` + cnDigits + `]{1,6})\s*[集话話]`)
	reBracket = regexp.MustCompile(`[\[【](\d{1,4})(?:v\d)?[\]】]`)
	reDash    = regexp.MustCompile(`\s-\s*(\d{1,4})(?:v\d)?(?:[\s\[(【.]|$)`)
	reNum     = regexp.MustCompile(`^(\d{1,4})$`)

	reSeasonCN = regexp.MustCompile(`第\s*([0-9]{1,2}|[` + cnDigits + `]{1,3})\s*季`)
	reSeasonEN = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:season[ ._]?|s)(\d{1,2})(?:[^a-z0-9]|$)`)
	reSeasonW  = regexp.MustCompile(`(?i)season[ ._]?\d{1,2}`)

	reGroup = regexp.MustCompile(`\[([^\]]*)\]|【([^】]*)】`)
	reCodec = regexp.MustCompile(`(?i)[hx]\.26[45]`) // 点转空格前先去掉 H.265
	reTag   = regexp.MustCompile(`(?i)^(?:\d{3,4}[pi]|[248]k|[hx]26[45]|hevc|avc|\d{1,2}bit|web-?dl|webrip|blu-?ray|bdrip|hdtv|dvdrip|remux|hdr|aac|flac|(?:19|20)\d{2}|\((?:19|20)\d{2}\)|s\d{1,2})$`)
)

// Parse 解析文件名（不含目录，可含扩展名）。未识别出集号时 ok 为 false。
// 模式按优先级：S01E02、EP02/E02、第02集（含中文数字）、[02]、` - 02 `、纯数字文件名（03 文档 4.6、06 文档 5.2）。
func Parse(name string) (info Info, ok bool) {
	stem := name
	if media.Kind(name) != media.KindOther {
		stem = strings.TrimSuffix(name, path.Ext(name))
	}
	stem = strings.TrimSpace(stem)

	if m := reSE.FindStringSubmatchIndex(stem); m != nil {
		info.Season, _ = strconv.Atoi(stem[m[4]:m[5]])
		info.Episode, _ = strconv.Atoi(stem[m[6]:m[7]])
		info.Show = normalizeShow(stem[:m[2]])
		return info, true
	}
	start := -1
	switch {
	case setFirst(reEP, stem, &info.Episode, &start):
	case setFirst(reDi, stem, &info.Episode, &start):
	case setFirst(reBracket, stem, &info.Episode, &start):
	case setFirst(reDash, stem, &info.Episode, &start):
	case setFirst(reNum, stem, &info.Episode, &start):
	default:
		return Info{}, false
	}
	info.Season = seasonOf(stem)
	prefix := stem[:start]
	info.Show = normalizeShow(prefix)
	if g := reGroup.FindAllStringSubmatch(prefix, -1); info.Show == "" && len(g) >= 2 {
		// [字幕组][剧名][07]：第一个方括号为组名，最后一个为剧名
		info.Show = normalizeShow(g[len(g)-1][1] + g[len(g)-1][2])
	}
	return info, true
}

// ParseSeason 解析只标季、不标集的文件名（如季级字幕 `Show.S01.chs.srt`、`剧名 第二季.ass`），
// 剧名取季号记号之前。能解析出集号、没有季号或没有剧名时 ok 为 false。
func ParseSeason(name string) (info Info, ok bool) {
	if _, hasEp := Parse(name); hasEp {
		return Info{}, false
	}
	stem := name
	if media.Kind(name) != media.KindOther {
		stem = strings.TrimSuffix(name, path.Ext(name))
	}
	for _, re := range []*regexp.Regexp{reSeasonCN, reSeasonEN} {
		if m := re.FindStringSubmatchIndex(stem); m != nil {
			n, ok := cnNum(stem[m[2]:m[3]])
			if show := normalizeShow(stem[:m[0]]); ok && show != "" {
				return Info{Show: show, Season: n}, true
			}
		}
	}
	return Info{}, false
}

// ParseWithDir 同 Parse；文件名中没有剧名时取所在目录名（dir 为相对路径，/ 或 \ 分隔）。
// 目录名是季文件夹（如 `第二季`、`Season 2`）时取其季号，剧名再往上取一级。
func ParseWithDir(dir, name string) (Info, bool) {
	info, ok := Parse(name)
	if !ok || info.Show != "" || dir == "" {
		return info, ok
	}
	segs := strings.Split(strings.ReplaceAll(dir, `\`, "/"), "/")
	for i := len(segs) - 1; i >= 0 && i >= len(segs)-2; i-- {
		if info.Season == 0 {
			info.Season = seasonOf(segs[i])
		}
		if info.Show = normalizeShow(segs[i]); info.Show != "" {
			break
		}
	}
	return info, ok
}

// setFirst 取 re 的第一个不是干扰数字（年份）的匹配，写入集号（最后一组）与记号起点
// （有两组时为第 1 组起点，否则为整个匹配起点）。
func setFirst(re *regexp.Regexp, s string, ep, start *int) bool {
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		num := s[m[len(m)-2]:m[len(m)-1]]
		n, ok := cnNum(num)
		if !ok || isYear(num) {
			continue
		}
		*ep, *start = n, m[0]
		if len(m) > 4 {
			*start = m[2]
		}
		return true
	}
	return false
}

func isYear(s string) bool {
	return len(s) == 4 && (strings.HasPrefix(s, "19") || strings.HasPrefix(s, "20"))
}

func seasonOf(s string) int {
	for _, re := range []*regexp.Regexp{reSeasonCN, reSeasonEN} {
		if m := re.FindStringSubmatch(s); m != nil {
			if n, ok := cnNum(m[1]); ok {
				return n
			}
		}
	}
	return 0
}

// cnNum 解析阿拉伯数字或中文数字（支持到百位，如 十二、二十、一百零五）。结果须大于 0。
func cnNum(s string) (int, bool) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, n > 0
	}
	total, cur := 0, 0
	for _, r := range s {
		switch r {
		case '十':
			total += max(cur, 1) * 10
			cur = 0
		case '百':
			total += max(cur, 1) * 100
			cur = 0
		default:
			d, ok := cnDigit[r]
			if !ok {
				return 0, false
			}
			cur = d
		}
	}
	total += cur
	return total, total > 0
}

// normalizeShow：去 [组名]/【组名】，去 H.265；点、下划线转空格；去中文季号、Season N；
// 按空白分词去掉分辨率、编码、来源、年份、季号标签；合并空白，去首尾空格与 -。
func normalizeShow(s string) string {
	s = reGroup.ReplaceAllString(s, " ")
	s = reCodec.ReplaceAllString(s, " ")
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = reSeasonCN.ReplaceAllString(s, " ")
	s = reSeasonW.ReplaceAllString(s, " ")
	var keep []string
	for _, f := range strings.Fields(s) {
		if !reTag.MatchString(f) {
			keep = append(keep, f)
		}
	}
	return strings.Trim(strings.Join(keep, " "), " -")
}
