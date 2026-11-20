package update

import (
	"fmt"
	"strconv"
	"strings"
)

type semver struct {
	nums [3]uint64
	pre  []string
}

// parseVersion 解析 MAJOR.MINOR.PATCH[-pre][+build]（可带 v 前缀）。
// 通过校验的版本只含 [0-9A-Za-z.-]，可安全用作目录名。
func parseVersion(s string) (semver, error) {
	var v semver
	core, _, _ := strings.Cut(strings.TrimPrefix(s, "v"), "+")
	core, pre, hasPre := strings.Cut(core, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("无效版本号 %q", s)
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || p == "" || p[0] == '+' {
			return v, fmt.Errorf("无效版本号 %q", s)
		}
		v.nums[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" || strings.TrimLeft(id, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-") != "" {
				return v, fmt.Errorf("无效版本号 %q", s)
			}
		}
	}
	return v, nil
}

// normalize 返回去掉 v 前缀与 +build 后缀的版本号。
func normalize(s string) (string, error) {
	if _, err := parseVersion(s); err != nil {
		return "", err
	}
	v, _, _ := strings.Cut(strings.TrimPrefix(s, "v"), "+")
	return v, nil
}

// Compare 按语义化版本比较 a 与 b：a<b 返回 -1，相等 0，a>b 返回 1。
func Compare(a, b string) (int, error) {
	va, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range va.nums {
		if va.nums[i] != vb.nums[i] {
			return cmp(va.nums[i] < vb.nums[i]), nil
		}
	}
	// 有预发布标识的版本低于正式版
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0, nil
	case len(va.pre) == 0:
		return 1, nil
	case len(vb.pre) == 0:
		return -1, nil
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		x, y := va.pre[i], vb.pre[i]
		if x == y {
			continue
		}
		nx, ex := strconv.ParseUint(x, 10, 64)
		ny, ey := strconv.ParseUint(y, 10, 64)
		switch {
		case ex == nil && ey == nil:
			return cmp(nx < ny), nil
		case ex == nil: // 数字标识低于字母标识
			return -1, nil
		case ey == nil:
			return 1, nil
		default:
			return cmp(x < y), nil
		}
	}
	if len(va.pre) == len(vb.pre) {
		return 0, nil
	}
	return cmp(len(va.pre) < len(vb.pre)), nil
}

func cmp(less bool) int {
	if less {
		return -1
	}
	return 1
}
