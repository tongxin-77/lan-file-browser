package listing

import (
	"slices"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	in := []string{"第10集.mkv", "第2集.mkv", "B.txt", "a.txt", "第1集.mkv", "ep02", "EP1", "x100", "x99", "file007", "file7a", "中文", "123456789012345678901234567890"}
	want := []string{"123456789012345678901234567890", "a.txt", "B.txt", "EP1", "ep02", "file007", "file7a", "x99", "x100", "中文", "第1集.mkv", "第2集.mkv", "第10集.mkv"} // 中文按码点：中 U+4E2D < 第 U+7B2C
	got := slices.Clone(in)
	slices.SortFunc(got, func(a, b string) int {
		if NaturalLess(a, b) {
			return -1
		}
		if NaturalLess(b, a) {
			return 1
		}
		return 0
	})
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestNaturalLessTieBreak(t *testing.T) {
	if NaturalLess("A", "a") == NaturalLess("a", "A") {
		t.Fatal("大小写仅不同时应有确定顺序")
	}
	if NaturalLess("x", "x") {
		t.Fatal("相等串不应 less")
	}
}
