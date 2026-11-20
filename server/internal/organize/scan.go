package organize

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lfb/server/internal/fsx"
)

// entry 为扫描发现的媒体/字幕文件。
type entry struct {
	Rel     string // 相对源文件夹，/ 分隔
	Size    int64
	Mtime   time.Time
	Kind    string
	Partial bool // 临时文件，或存在同名 .aria2（下载中）
}

// skipName 报告扫描时跳过的名字：始终隐藏项、隐藏文件、.lfb-*（06 文档 4.1）。
func skipName(fi fs.FileInfo) bool {
	n := fi.Name()
	return fsx.IsAlwaysHidden(n) || fsx.IsHidden(fi) || strings.HasPrefix(strings.ToLower(n), ".lfb-")
}

// isLink 报告目录项是否为符号链接、目录联接等重解析点（一律不跟随，06 文档第 9 节）。
func isLink(fi fs.FileInfo) bool {
	if fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return true
	}
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return ok && fi.IsDir() && d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// scanSource 递归遍历源文件夹，只做 Lstat，返回媒体与字幕文件（含临时文件，标 Partial）。
// 下载中的临时文件本身（.part 等）不是媒体扩展名，不会出现在结果中；它对应的 .aria2 会让同名文件标 Partial。
func scanSource(root string) ([]entry, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	var out []entry
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		f, err := os.Open(dir)
		if err != nil {
			return
		}
		infos, _ := f.Readdir(-1) // Windows 上 Readdir 返回 Lstat 语义的信息
		f.Close()
		names := map[string]bool{}
		for _, fi := range infos {
			names[strings.ToLower(fi.Name())] = true
		}
		for _, fi := range infos {
			if skipName(fi) || isLink(fi) {
				continue
			}
			r := path.Join(rel, fi.Name())
			if fi.IsDir() {
				walk(filepath.Join(dir, fi.Name()), r)
				continue
			}
			if !fi.Mode().IsRegular() {
				continue
			}
			kind := kindOf(fi.Name())
			if kind == "" {
				continue
			}
			out = append(out, entry{Rel: r, Size: fi.Size(), Mtime: fi.ModTime(), Kind: kind,
				Partial: isTempName(fi.Name()) || names[strings.ToLower(fi.Name())+".aria2"]})
		}
	}
	walk(root, "")
	return out, nil
}
