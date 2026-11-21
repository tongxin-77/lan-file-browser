package main

import (
	"context"

	"golang.org/x/sys/windows"
)

// shutdownEventName 是安装 / 卸载程序请求服务端退出的命名事件，须与 installer/lfb-server.iss 一致。
// 程序无窗口，Restart Manager 关不掉；安装程序虽提权运行，但与用户在同一登录会话，Local\ 命名空间互通。
const shutdownEventName = `Local\LanFileBrowserShutdown`

// notifyShutdownEvent 创建命名事件 name，返回在该事件被置位时取消的 ctx。
// 事件句柄在进程生命周期内保持打开。
func notifyShutdownEvent(parent context.Context, name string) (context.Context, context.CancelFunc, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, nil, err
	}
	h, err := windows.CreateEvent(nil, 1, 0, p) // 手动重置；已存在（另一实例）时仍返回有效句柄
	if h == 0 {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	go func() {
		if ev, _ := windows.WaitForSingleObject(h, windows.INFINITE); ev == windows.WAIT_OBJECT_0 {
			cancel()
		}
	}()
	return ctx, cancel, nil
}
