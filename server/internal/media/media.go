// Package media 按扩展名判定文件类别与 MIME（03 文档 4.2、4.3）。
// 不用 mime.TypeByExtension：Windows 上读注册表，结果因机器而异。
package media

import (
	"path"
	"strings"
)

const (
	KindDir      = "dir"
	KindVideo    = "video"
	KindImage    = "image"
	KindAudio    = "audio"
	KindSubtitle = "subtitle"
	KindOther    = "other"
)

type typeInfo struct{ kind, mime string }

// types：扩展名（小写、不含点）→ 类别与 MIME。mime 为空表示 application/octet-stream。
var types = map[string]typeInfo{
	"mp4": {KindVideo, "video/mp4"}, "m4v": {KindVideo, "video/mp4"},
	"mkv":  {KindVideo, "video/x-matroska"},
	"webm": {KindVideo, "video/webm"},
	"avi":  {KindVideo, "video/x-msvideo"},
	"ts":   {KindVideo, "video/mp2t"}, "m2ts": {KindVideo, "video/mp2t"},
	"mov": {KindVideo, ""}, "wmv": {KindVideo, ""}, "flv": {KindVideo, ""}, "rmvb": {KindVideo, ""}, "3gp": {KindVideo, ""},

	"jpg": {KindImage, "image/jpeg"}, "jpeg": {KindImage, "image/jpeg"},
	"png":  {KindImage, "image/png"},
	"webp": {KindImage, "image/webp"},
	"gif":  {KindImage, "image/gif"},
	"bmp":  {KindImage, ""}, "heic": {KindImage, ""}, "heif": {KindImage, ""},

	"mp3":  {KindAudio, "audio/mpeg"},
	"flac": {KindAudio, "audio/flac"},
	"m4a":  {KindAudio, "audio/mp4"},
	"aac":  {KindAudio, ""}, "wav": {KindAudio, ""}, "ogg": {KindAudio, ""}, "opus": {KindAudio, ""}, "wma": {KindAudio, ""},

	"srt": {KindSubtitle, "application/x-subrip"},
	"vtt": {KindSubtitle, "text/vtt"},
	"ass": {KindSubtitle, "text/x-ssa"}, "ssa": {KindSubtitle, "text/x-ssa"},
}

func lookup(name string) typeInfo {
	return types[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
}

// Kind 返回文件（非目录）的类别：video/image/audio/subtitle/other。
func Kind(name string) string {
	if k := lookup(name).kind; k != "" {
		return k
	}
	return KindOther
}

// MIME 返回文件的 Content-Type，未知扩展名为 application/octet-stream。
func MIME(name string) string {
	if m := lookup(name).mime; m != "" {
		return m
	}
	return "application/octet-stream"
}
