package fsx

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// newShare 构建测试共享目录与一个共享外目录，返回 (共享根, 共享外目录)。
func newShare(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	share := filepath.Join(base, "share")
	outside := filepath.Join(base, "outside")
	files := map[string]string{
		"a.txt":                       "A",
		"sub/b.txt":                   "B",
		"中文目录/视频 1.mp4":               "V",
		"with space.txt":              "S",
		"Long Directory Name/f.txt":   "L",
		"$RECYCLE.BIN/x.txt":          "R",
		"System Volume Information/x": "SVI",
		"desktop.ini":                 "D",
		"Thumbs.db":                   "T",
		"sub/DESKTOP.INI":             "D2",
		".lfb-upload-abc":             "U",
	}
	for rel, content := range files {
		writeFile(t, filepath.Join(share, rel), content)
	}
	writeFile(t, filepath.Join(outside, "secret.txt"), "SECRET")
	return share, outside
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOpenRejectsAttacks(t *testing.T) {
	share, _ := newShare(t)
	cases := []struct {
		path string
		want error
	}{
		// 目录穿越
		{"../outside/secret.txt", ErrBadPath},
		{"a/../../b", ErrBadPath},
		{"sub/../a.txt", ErrBadPath},
		{"./a.txt", ErrBadPath},
		// 绝对路径 / 空段
		{"/etc/x", ErrBadPath},
		{"a//b", ErrBadPath},
		{"sub/", ErrBadPath},
		// 盘符
		{"C:/Windows", ErrBadPath},
		{"C:x", ErrBadPath},
		// UNC / 设备命名空间
		{`\\server\share\x`, ErrBadPath},
		{`\\?\C:\`, ErrBadPath},
		{`\\.\COM1`, ErrBadPath},
		// 反斜杠绕过
		{`..\..\x`, ErrBadPath},
		{`sub\..\..\outside`, ErrBadPath},
		// NTFS 备用数据流
		{"a.txt:secret", ErrBadPath},
		{"a.txt::$DATA", ErrBadPath},
		// 设备名（带扩展名、大小写、尾空格）
		{"CON", ErrBadPath},
		{"nul.txt", ErrBadPath},
		{"COM1", ErrBadPath},
		{"com0", ErrBadPath},
		{"sub/lpt9.mp4", ErrBadPath},
		{"LPT¹", ErrBadPath},
		{"com³.txt", ErrBadPath},
		{"CONIN$", ErrBadPath},
		{"conout$", ErrBadPath},
		{"Aux .txt", ErrBadPath},
		{"PRN", ErrBadPath},
		// 结尾点和空格
		{"a.txt.", ErrBadPath},
		{"a.txt ", ErrBadPath},
		{"sub./b.txt", ErrBadPath},
		// 空字节与控制/保留字符
		{"a\x00.mp4", ErrBadPath},
		{"a\x1f.mp4", ErrBadPath},
		{"a<b", ErrBadPath},
		{"a>b", ErrBadPath},
		{`a"b`, ErrBadPath},
		{"a|b", ErrBadPath},
		{"a?b", ErrBadPath},
		{"a*b", ErrBadPath},
		// 非法 UTF-8 / 超长
		{"\xff\xfe", ErrBadPath},
		{strings.Repeat("a/", 2048) + "b", ErrBadPath},
		{strings.Repeat("长", 256), ErrBadPath},
		// 始终隐藏的系统项（大小写不敏感）
		{"$RECYCLE.BIN", ErrNotFound},
		{"$recycle.bin/x.txt", ErrNotFound},
		{"System Volume Information", ErrNotFound},
		{"system volume information/x", ErrNotFound},
		{"desktop.ini", ErrNotFound},
		{"DESKTOP.INI", ErrNotFound},
		{"thumbs.db", ErrNotFound},
		{"sub/desktop.ini", ErrNotFound},
		{".lfb-upload-abc", ErrNotFound},
		// 不存在
		{"missing.txt", ErrNotFound},
		{"sub/missing/x", ErrNotFound},
	}
	for _, c := range cases {
		f, err := Open(share, c.path)
		if err == nil {
			f.Close()
		}
		if !errors.Is(err, c.want) {
			t.Errorf("Open(%q) = %v, want %v", c.path, err, c.want)
		}
	}
}

func TestOpenRejectsShortNames(t *testing.T) {
	share, _ := newShare(t)
	for _, long := range []string{"Long Directory Name", "$RECYCLE.BIN"} {
		short := shortName(t, filepath.Join(share, long))
		if short == long {
			t.Skipf("卷未启用 8.3 短文件名，无法测试")
		}
		f, err := Open(share, short)
		if err == nil {
			f.Close()
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) [短名 of %q] = %v, want NOT_FOUND", short, long, err)
		}
	}
}

func shortName(t *testing.T, p string) string {
	t.Helper()
	src, _ := windows.UTF16PtrFromString(p)
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(src, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Fatalf("GetShortPathName: %v", err)
	}
	return filepath.Base(windows.UTF16ToString(buf[:n]))
}

func TestOpenAllowsLegit(t *testing.T) {
	share, _ := newShare(t)
	files := map[string]string{
		"a.txt":          "A",
		"A.TXT":          "A",
		"sub/b.txt":      "B",
		`sub\b.txt`:      "B",
		"中文目录/视频 1.mp4":  "V",
		"with space.txt": "S",
	}
	for p, want := range files {
		f, err := Open(share, p)
		if err != nil {
			t.Errorf("Open(%q) = %v", p, err)
			continue
		}
		if got := readAll(t, f); got != want {
			t.Errorf("Open(%q) 内容 %q, want %q", p, got, want)
		}
	}
	for _, dir := range []string{"", "sub", "中文目录"} {
		f, err := Open(share, dir)
		if err != nil {
			t.Errorf("Open(%q) = %v", dir, err)
			continue
		}
		fi, _ := f.Stat()
		f.Close()
		if !fi.IsDir() {
			t.Errorf("Open(%q) 应为目录", dir)
		}
	}
}

func TestOpenOfflineShare(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "gone"), "a.txt")
	if !errors.Is(err, ErrShareOffline) {
		t.Fatalf("got %v, want SHARE_OFFLINE", err)
	}
}

func mklinkJ(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
}

func TestJunctions(t *testing.T) {
	share, outside := newShare(t)
	mklinkJ(t, filepath.Join(share, "jin"), filepath.Join(share, "sub"))
	mklinkJ(t, filepath.Join(share, "jout"), outside)
	mklinkJ(t, filepath.Join(share, "jrec"), filepath.Join(share, "$RECYCLE.BIN"))
	mklinkJ(t, filepath.Join(share, "sub", "jup"), share) // 指回共享根

	// jin/jup/sub/b.txt：jin→sub，sub/jup→共享根，链式跳转
	ok := map[string]string{"jin/b.txt": "B", "sub/jup/a.txt": "A", "jin/jup/sub/b.txt": "B"}
	for p, want := range ok {
		f, err := Open(share, p)
		if err != nil {
			t.Errorf("Open(%q) = %v", p, err)
			continue
		}
		if got := readAll(t, f); got != want {
			t.Errorf("Open(%q) 内容 %q, want %q", p, got, want)
		}
	}
	for _, p := range []string{"jout", "jout/secret.txt", "jrec", "jrec/x.txt"} {
		f, err := Open(share, p)
		if err == nil {
			f.Close()
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) = %v, want NOT_FOUND", p, err)
		}
	}

	// 共享根本身是联接
	rootLink := filepath.Join(filepath.Dir(share), "rootlink")
	mklinkJ(t, rootLink, share)
	f, err := Open(rootLink, "sub/b.txt")
	if err != nil {
		t.Fatalf("共享根为联接: %v", err)
	}
	f.Close()
}

func TestSymlinks(t *testing.T) {
	share, outside := newShare(t)
	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(share, name)); err != nil {
			t.Skipf("无权限创建符号链接（需管理员或开发者模式）: %v", err)
		}
	}
	link("sub", "relin")                                           // 相对，共享内目录
	link(`..\outside\secret.txt`, "relout")                        // 相对，逃逸
	link(filepath.Join(share, "sub", "b.txt"), "absin")            // 绝对，共享内文件
	link(filepath.Join(outside, "secret.txt"), "absout")           // 绝对，共享外
	link(filepath.Join(share, "desktop.ini"), "visible-to-hidden") // 指向始终隐藏项

	for p, want := range map[string]string{"relin/b.txt": "B", "absin": "B"} {
		f, err := Open(share, p)
		if err != nil {
			t.Errorf("Open(%q) = %v", p, err)
			continue
		}
		if got := readAll(t, f); got != want {
			t.Errorf("Open(%q) 内容 %q, want %q", p, got, want)
		}
	}
	for _, p := range []string{"relout", "absout", "visible-to-hidden"} {
		f, err := Open(share, p)
		if err == nil {
			f.Close()
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) = %v, want NOT_FOUND", p, err)
		}
	}
}

func TestIsAlwaysHidden(t *testing.T) {
	for _, n := range []string{"$RECYCLE.BIN", "$recycle.bin", "System Volume Information", "Desktop.ini", "THUMBS.DB", ".lfb-upload-x1", ".LFB-copying-ab"} {
		if !IsAlwaysHidden(n) {
			t.Errorf("IsAlwaysHidden(%q) = false", n)
		}
	}
	for _, n := range []string{"a.txt", "desktop.ini.bak", "recycle.bin", ".lfb-copy"} {
		if IsAlwaysHidden(n) {
			t.Errorf("IsAlwaysHidden(%q) = true", n)
		}
	}
}

func TestIsHidden(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.txt")
	dot := filepath.Join(dir, ".dot")
	attr := filepath.Join(dir, "attr.txt")
	for _, p := range []string{plain, dot, attr} {
		writeFile(t, p, "x")
	}
	p16, _ := windows.UTF16PtrFromString(attr)
	if err := windows.SetFileAttributes(p16, windows.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{plain: false, dot: true, attr: true} {
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := IsHidden(fi); got != want {
			t.Errorf("IsHidden(%s) = %v, want %v", filepath.Base(p), got, want)
		}
	}
}

// 本机卷可能未启用 8.3 短名：直接验证第 3 层对“请求短名 ≠ 最终长名”的判定。
func TestCheckFinalShortNameMismatch(t *testing.T) {
	share, _ := newShare(t)
	root, err := os.OpenRoot(share)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rootFinal, err := rootFinalPath(root)
	if err != nil {
		t.Fatal(err)
	}
	f, err := root.Open("Long Directory Name")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := checkFinal(f, rootFinal, []string{"LONGDI~1"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("短名请求: got %v, want NOT_FOUND（与不存在一致，不泄露存在性）", err)
	}
	if _, err := checkFinal(f, rootFinal, []string{"other"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("名称不一致: got %v, want NOT_FOUND", err)
	}
	if _, err := checkFinal(f, rootFinal, []string{"long directory name"}); err != nil {
		t.Errorf("仅大小写不同应允许: %v", err)
	}
}
