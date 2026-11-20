package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 首次运行判定：数据目录下没有 config.json 时才自动打开管理页。
func TestConfigExists(t *testing.T) {
	dir := t.TempDir()
	if configExists(dir) {
		t.Fatal("空目录不应视为已有配置")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !configExists(dir) {
		t.Fatal("已有 config.json 应视为非首次运行")
	}
}

// Shutdown 超时（有请求卡住）后应强制 Close 并正常返回，而非报错退出。
func TestShutdownForceClose(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	started := make(chan struct{})
	hs := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-block
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go hs.Serve(ln)
	go http.Get("http://" + ln.Addr().String())
	<-started

	start := time.Now()
	shutdown(hs, 100*time.Millisecond)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("shutdown 耗时过长: %v", d)
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		t.Fatal("强制关闭后监听应已关闭")
	}
}
