package fsx

import (
	"io/fs"
	"strings"
	"syscall"
)

var alwaysHidden = []string{"$RECYCLE.BIN", "System Volume Information", "desktop.ini", "thumbs.db"}

// 上传（03 §4.9）与自动整理（06 第 7 节）的临时文件前缀。
var tempPrefixes = []string{".lfb-upload-", ".lfb-copying-"}

// IsAlwaysHidden 报告名称是否属于始终隐藏的系统项（大小写不敏感）。
func IsAlwaysHidden(name string) bool {
	for _, h := range alwaysHidden {
		if strings.EqualFold(name, h) {
			return true
		}
	}
	for _, p := range tempPrefixes {
		if len(name) >= len(p) && strings.EqualFold(name[:len(p)], p) {
			return true
		}
	}
	return false
}

// IsHidden 报告文件是否为隐藏文件：Windows Hidden 属性，或名称以 "." 开头。
func IsHidden(fi fs.FileInfo) bool {
	if strings.HasPrefix(fi.Name(), ".") {
		return true
	}
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return ok && d.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
}
