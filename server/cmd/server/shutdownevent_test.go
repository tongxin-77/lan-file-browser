package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// 安装程序 OpenEvent + SetEvent 后，服务端的 ctx 被取消（走优雅退出）。
func TestNotifyShutdownEvent(t *testing.T) {
	name := fmt.Sprintf(`Local\LanFileBrowserShutdownTest-%d`, os.Getpid())
	ctx, cancel, err := notifyShutdownEvent(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatal("事件未置位时 ctx 不应取消")
	case <-time.After(100 * time.Millisecond):
	}

	p, _ := windows.UTF16PtrFromString(name)
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, p)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err := windows.SetEvent(h); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("事件置位后 ctx 未取消")
	}
}
