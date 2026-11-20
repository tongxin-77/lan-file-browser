package organize

import (
	"path/filepath"
	"strings"

	"lfb/server/internal/config"
)

// LayoutError 为源文件夹/库目录布局错误（06 文档第 9 节）。Conflict 为 true 表示与其他片库的源文件夹重叠（409）。
type LayoutError struct {
	Conflict bool
	Msg      string
}

func (e *LayoutError) Error() string { return e.Msg }

// canon 返回规范化后的真实路径（解析链接/联接/8.3 短名）；路径不存在时解析最近的已存在祖先再拼回剩余部分。
func canon(p string) string {
	p = filepath.Clean(p)
	if r, err := realPath(p); err == nil {
		p = r
	} else if dir := filepath.Dir(p); dir != p {
		p = canon(dir) + `\` + filepath.Base(p)
	}
	return strings.TrimRight(strings.ToLower(filepath.Clean(p)), `\`)
}

// overlap 报告两个规范路径是否相同或互相包含（按分隔符边界比较）。
func overlap(a, b string) bool {
	return a == b || strings.HasPrefix(b, a+`\`) || strings.HasPrefix(a, b+`\`)
}

// Nested 报告两个目录（真实路径、大小写不敏感）是否相同或互相包含。
func Nested(a, b string) bool { return overlap(canon(a), canon(b)) }

// CheckLayout 校验所有配置了源文件夹的片库：库目录与源文件夹互不包含（含跨片库），
// 同一片库的源文件夹互不重叠，不同片库的源文件夹互不重叠（Conflict）。
func CheckLayout(cfg *config.Config) error {
	type lib struct {
		id   string
		root string
		srcs []string
	}
	var libs []lib
	for _, sh := range cfg.Shares {
		if sh.Organize == nil || len(sh.Organize.Sources) == 0 {
			continue
		}
		l := lib{id: sh.ID, root: canon(sh.Path)}
		for _, s := range sh.Organize.Sources {
			l.srcs = append(l.srcs, canon(s.Path))
		}
		libs = append(libs, l)
	}
	for i, a := range libs {
		for k, s := range a.srcs {
			for _, b := range libs {
				if overlap(s, b.root) {
					if b.id == a.id {
						return &LayoutError{Msg: "源文件夹和片库文件夹不能相同，也不能一个在另一个里面"}
					}
					return &LayoutError{Msg: "源文件夹和另一个自动整理片库的文件夹不能相同，也不能一个在另一个里面"}
				}
			}
			for k2 := k + 1; k2 < len(a.srcs); k2++ {
				if overlap(s, a.srcs[k2]) {
					return &LayoutError{Msg: "两个源文件夹不能相同，也不能一个在另一个里面"}
				}
			}
			for _, b := range libs[i+1:] {
				for _, s2 := range b.srcs {
					if overlap(s, s2) {
						return &LayoutError{Conflict: true, Msg: "这个源文件夹已被另一个片库的自动整理使用"}
					}
				}
			}
		}
	}
	return nil
}

// PathCheck 为 check-path 的结果。
type PathCheck struct {
	SameDisk   bool `json:"sameDisk"`
	SameVolume bool `json:"sameVolume"`
	Nested     bool `json:"nested"`
}

// CheckPath 判断源文件夹与库目录是否同盘、同卷、互相包含（06 文档第 1、11 节）。
func (m *Manager) CheckPath(sharePath, sourcePath string) PathCheck {
	return PathCheck{
		SameDisk:   sameDisk(m.o.DiskOf(sharePath), m.o.DiskOf(sourcePath)),
		SameVolume: m.o.SameVolume(sharePath, sourcePath),
		Nested:     Nested(sharePath, sourcePath),
	}
}
