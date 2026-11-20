// Package fsx 实现共享目录内的安全文件访问（03 文档第 2 节）。
package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var (
	ErrBadPath      = errors.New("BAD_PATH")
	ErrNotFound     = errors.New("NOT_FOUND")
	ErrShareOffline = errors.New("SHARE_OFFLINE")
)

const maxLinkHops = 8

// Open 安全打开共享根 shareRoot 下的相对路径 rel（"/" 或 "\" 分隔，空串为根）。
// 三层防护：词法校验 → os.Root → 最终路径校验。不处理普通隐藏文件（由调用方按权限过滤）。
func Open(shareRoot, rel string) (*os.File, error) {
	f, _, err := OpenFinal(shareRoot, rel)
	return f, err
}

// OpenFinal 同 Open，另返回解析链接后的真实最终路径（相对共享根的长名分段，根为空），
// 供调用方按真实路径做隐藏过滤。
func OpenFinal(shareRoot, rel string) (*os.File, []string, error) {
	segs, err := validateRel(rel)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range segs {
		if IsAlwaysHidden(s) {
			return nil, nil, ErrNotFound
		}
	}

	realRoot, err := filepath.EvalSymlinks(shareRoot)
	if err != nil {
		return nil, nil, ErrShareOffline
	}
	root, err := os.OpenRoot(realRoot) // 每请求打开，避免长期占住移动硬盘
	if err != nil {
		return nil, nil, ErrShareOffline
	}
	defer root.Close() // 已打开的文件不受影响

	rootFinal, err := rootFinalPath(root)
	if err != nil {
		return nil, nil, ErrShareOffline
	}
	eff, err := resolveLinks(root, realRoot, segs)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	f, err := root.Open(joinRel(eff))
	if err != nil {
		return nil, nil, ErrNotFound
	}

	final, err := checkFinal(f, rootFinal, eff)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, final, nil
}

// resolveLinks 逐段 Lstat，遇到符号链接/联接时读取目标、确认仍在共享内并换算为共享内相对路径。
// 返回不含链接的相对路径段，交给 os.Root 打开。
func resolveLinks(root *os.Root, realRoot string, segs []string) ([]string, error) {
	var cur []string
	hops := 0
	for _, seg := range segs {
		cur = append(cur, seg)
		p := joinRel(cur)
		fi, err := root.Lstat(p)
		if err != nil {
			return nil, err
		}
		mode := fi.Mode()
		if mode&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
			continue
		}
		target, err := root.Readlink(p)
		if err != nil {
			if mode&fs.ModeSymlink == 0 {
				continue // 非链接类重解析点（如云文件占位），按普通文件处理，第 3 层兜底
			}
			return nil, err
		}
		if hops++; hops > maxLinkHops {
			return nil, ErrNotFound
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(realRoot, joinRel(cur[:len(cur)-1]), target)
		}
		target, err = filepath.EvalSymlinks(target)
		if err != nil {
			return nil, err
		}
		r, err := filepath.Rel(realRoot, target)
		if err != nil || r == ".." || strings.HasPrefix(r, `..\`) || filepath.IsAbs(r) {
			return nil, ErrNotFound
		}
		cur = nil
		if r != "." {
			cur = strings.Split(r, `\`)
		}
	}
	return cur, nil
}

// checkFinal 是第 3 层：最终路径须在共享根内、与请求的相对路径一致（消解 8.3 短名），且不含始终隐藏项。
// 返回最终路径相对共享根的分段。不一致（含短名）一律 NOT_FOUND，与不存在无法区分。
func checkFinal(f *os.File, rootFinal string, eff []string) ([]string, error) {
	final, err := finalPath(f)
	if err != nil {
		return nil, ErrNotFound
	}
	var finalSegs []string
	switch {
	case strings.EqualFold(final, rootFinal):
	case len(final) > len(rootFinal)+1 && strings.EqualFold(final[:len(rootFinal)], rootFinal) && final[len(rootFinal)] == '\\':
		finalSegs = strings.Split(final[len(rootFinal)+1:], `\`)
	default:
		return nil, ErrNotFound
	}
	if !equalFoldSegs(finalSegs, eff) {
		return nil, ErrNotFound
	}
	for _, s := range finalSegs {
		if IsAlwaysHidden(s) {
			return nil, ErrNotFound
		}
	}
	return finalSegs, nil
}

func rootFinalPath(root *os.Root) (string, error) {
	d, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer d.Close()
	p, err := finalPath(d)
	return strings.TrimSuffix(p, `\`), err // 盘符根形如 \?\D:\
}

// finalPath 通过 GetFinalPathNameByHandleW 取打开句柄的真实长路径。
func finalPath(f *os.File) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
}

func equalFoldSegs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func joinRel(segs []string) string {
	if len(segs) == 0 {
		return "."
	}
	return filepath.Join(segs...)
}
