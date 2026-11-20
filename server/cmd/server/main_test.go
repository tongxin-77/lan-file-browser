package main

import (
	"net"
	"net/http"
	"testing"
	"time"
)

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
