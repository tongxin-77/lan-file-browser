package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 无控制台时日志写入 <数据目录>\logs\server.log，追加写入。
func TestOpenLogFileAppends(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"first\n", "second\n"} {
		f, err := openLogFile(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	b, err := os.ReadFile(filepath.Join(dir, "logs", "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "first\nsecond\n" {
		t.Fatalf("日志内容 = %q", b)
	}
}

// 超过上限时把旧日志改名为 server.log.1（覆盖更早的），新日志从空文件开始。
func TestOpenLogFileRotates(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	old := strings.Repeat("x", maxLogSize+1)
	os.WriteFile(filepath.Join(logs, "server.log"), []byte(old), 0o600)
	os.WriteFile(filepath.Join(logs, "server.log.1"), []byte("older"), 0o600)

	f, err := openLogFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("new\n")
	f.Close()

	if b, _ := os.ReadFile(filepath.Join(logs, "server.log")); string(b) != "new\n" {
		t.Fatalf("server.log = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(logs, "server.log.1")); string(b) != old {
		t.Fatalf("server.log.1 长度 = %d", len(b))
	}
}
