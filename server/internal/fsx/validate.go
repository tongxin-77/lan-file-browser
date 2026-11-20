package fsx

import (
	"strings"
	"unicode/utf8"
)

const (
	maxPathBytes = 4096
	maxSegRunes  = 255
)

// Windows 保留设备名（带扩展名同样生效），比较前转大写。
var deviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM¹": true, "COM²": true, "COM³": true, "LPT¹": true, "LPT²": true, "LPT³": true,
}

func init() {
	for c := '0'; c <= '9'; c++ {
		deviceNames["COM"+string(c)] = true
		deviceNames["LPT"+string(c)] = true
	}
}

// validateRel 是第 1 层词法校验（03 文档 2.2），返回分段后的相对路径；空串表示共享根。
func validateRel(rel string) ([]string, error) {
	if !utf8.ValidString(rel) || len(rel) > maxPathBytes {
		return nil, ErrBadPath
	}
	if rel == "" {
		return nil, nil
	}
	rel = strings.ReplaceAll(rel, `\`, "/")
	if strings.HasPrefix(rel, "/") {
		return nil, ErrBadPath
	}
	segs := strings.Split(rel, "/")
	for _, s := range segs {
		if !validSeg(s) {
			return nil, ErrBadPath
		}
	}
	return segs, nil
}

func validSeg(s string) bool {
	if s == "" || s == "." || s == ".." || utf8.RuneCountInString(s) > maxSegRunes {
		return false
	}
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || strings.ContainsRune(`:<>"|?*`, r) {
			return false
		}
	}
	base, _, _ := strings.Cut(s, ".")
	return !deviceNames[strings.ToUpper(strings.TrimRight(base, " "))]
}
