package activity

import (
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

// SetThreadExecutionState 标志（winbase.h）。不用 ES_DISPLAY_REQUIRED / ES_AWAYMODE_REQUIRED（05 文档 3.4）。
const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// SystemKeeper 用 SetThreadExecutionState 阻止系统睡眠。该状态属于调用线程，
// 故在一个 runtime.LockOSThread 的专用 goroutine 中串行执行（05 文档 3.4）。
type SystemKeeper struct {
	req  chan bool
	done chan struct{}
	once sync.Once
}

func NewSystemKeeper() *SystemKeeper {
	k := &SystemKeeper{req: make(chan bool), done: make(chan struct{})}
	go k.loop()
	return k
}

func (k *SystemKeeper) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for {
		select {
		case on := <-k.req:
			flags := uintptr(esContinuous)
			if on {
				flags |= esSystemRequired
			}
			procSetThreadExecutionState.Call(flags) // 返回值为旧状态，0 表示失败；失败无可补救，忽略
		case <-k.done:
			procSetThreadExecutionState.Call(esContinuous)
			return
		}
	}
}

// SetAwake 阻止（true）或恢复（false）系统睡眠。Close 之后调用无效。
func (k *SystemKeeper) SetAwake(on bool) {
	select {
	case k.req <- on:
	case <-k.done:
	}
}

// Close 恢复睡眠并结束专用 goroutine。
func (k *SystemKeeper) Close() { k.once.Do(func() { close(k.done) }) }
