// Package listing 提供目录列表用的自然排序（03 文档 4.2）。
package listing

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// NaturalLess 按自然顺序比较：连续数字按数值比较，其余字符大小写不敏感按码点比较。
// 例如 "第2集" < "第10集"、"a.txt" < "B.txt"。完全等价时按原串比较，保证顺序确定。
func NaturalLess(a, b string) bool {
	if c := naturalCompare(a, b); c != 0 {
		return c < 0
	}
	return a < b
}

func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			da, db := digitRun(a), digitRun(b)
			if c := compareNum(da, db); c != 0 {
				return c
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if la, lb := unicode.ToLower(ra), unicode.ToLower(rb); la != lb {
			if la < lb {
				return -1
			}
			return 1
		}
		a, b = a[na:], b[nb:]
	}
	return len(a) - len(b)
}

// compareNum 比较两个十进制数字串的数值（忽略前导零，不受整数溢出影响）。
func compareNum(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

func digitRun(s string) string {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i]
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }
