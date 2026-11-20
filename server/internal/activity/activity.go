// Package activity 跟踪活跃播放（05 文档 3.4），并在有活跃播放时阻止系统睡眠。
//
// 「活跃播放」= 存在进行中的 /api/file 视频响应，或 60 秒内收到过 /api/progress。
// 每条记录带文件所在卷（如 `D:\`，见 VolumeOf），供整理库判断“同盘播放时暂停复制”（06 文档第 8 节）。
package activity

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	DefaultResumeAfter    = 5 * time.Minute  // 所有播放结束后多久恢复睡眠（05 文档 3.4）
	DefaultProgressWindow = 60 * time.Second // 进度上报的活跃窗口
	tickInterval          = 15 * time.Second
)

// Keeper 设置“阻止系统睡眠”状态；Windows 实现见 NewSystemKeeper，测试注入假实现。
type Keeper interface {
	SetAwake(on bool)
}

type Options struct {
	Now            func() time.Time // 默认 time.Now
	Keeper         Keeper           // nil = 不控制睡眠
	ResumeAfter    time.Duration    // 默认 DefaultResumeAfter
	ProgressWindow time.Duration    // 默认 DefaultProgressWindow
}

// Tracker 并发安全。
type Tracker struct {
	mu          sync.Mutex
	now         func() time.Time
	keeper      Keeper
	resumeAfter time.Duration
	window      time.Duration

	streams  map[string]int       // 卷键 → 进行中的视频流数
	progress map[string]time.Time // 卷键 → 最近一次进度上报
	names    map[string]string    // 卷键 → 原始卷标识（首次出现的写法）
	idleFrom time.Time            // 最近一次“活跃结束”的时刻（流结束 / 进度窗口到期）
	awake    bool
}

func New(o Options) *Tracker {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ResumeAfter <= 0 {
		o.ResumeAfter = DefaultResumeAfter
	}
	if o.ProgressWindow <= 0 {
		o.ProgressWindow = DefaultProgressWindow
	}
	return &Tracker{now: o.Now, keeper: o.Keeper, resumeAfter: o.ResumeAfter, window: o.ProgressWindow,
		streams: map[string]int{}, progress: map[string]time.Time{}, names: map[string]string{}}
}

func volKey(v string) string { return strings.ToLower(v) }

// StreamStart 记录一路视频流开始，返回结束函数（可重复调用，只生效一次）。
func (t *Tracker) StreamStart(volume string) (end func()) {
	k := volKey(volume)
	t.mu.Lock()
	t.streams[k]++
	if _, ok := t.names[k]; !ok {
		t.names[k] = volume
	}
	t.updateLocked()
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			if t.streams[k]--; t.streams[k] <= 0 {
				delete(t.streams, k)
			}
			if t.idleFrom.Before(t.now()) {
				t.idleFrom = t.now()
			}
			t.updateLocked()
			t.mu.Unlock()
		})
	}
}

// Progress 记录一次 /api/progress 上报（由进度处理函数调用）。
func (t *Tracker) Progress(volume string) {
	k := volKey(volume)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.progress[k] = t.now()
	if _, ok := t.names[k]; !ok {
		t.names[k] = volume
	}
	if end := t.now().Add(t.window); t.idleFrom.Before(end) {
		t.idleFrom = end
	}
	t.updateLocked()
}

// Active 报告当前是否有任何活跃播放。
func (t *Tracker) Active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.activeLocked()) > 0
}

// ActiveOn 报告指定卷（大小写不敏感）上是否有活跃播放。
func (t *Tracker) ActiveOn(volume string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Contains(t.activeLocked(), volKey(volume))
}

// ActiveVolumes 返回有活跃播放的卷（原始写法，排序）。
func (t *Tracker) ActiveVolumes() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for _, k := range t.activeLocked() {
		out = append(out, t.names[k])
	}
	slices.Sort(out)
	return out
}

// activeLocked 返回活跃卷键，并清理过期的进度记录。
func (t *Tracker) activeLocked() []string {
	now := t.now()
	var keys []string
	for k := range t.streams {
		keys = append(keys, k)
	}
	for k, at := range t.progress {
		if now.Sub(at) >= t.window {
			delete(t.progress, k)
			continue
		}
		if t.streams[k] == 0 {
			keys = append(keys, k)
		}
	}
	for k := range t.names {
		if t.streams[k] == 0 && t.progress[k].IsZero() {
			delete(t.names, k)
		}
	}
	return keys
}

// Tick 重新评估睡眠状态（Run 定时调用；测试中配合注入的时钟直接调用）。
func (t *Tracker) Tick() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.updateLocked()
}

func (t *Tracker) updateLocked() {
	want := len(t.activeLocked()) > 0 || t.awake && t.now().Before(t.idleFrom.Add(t.resumeAfter))
	t.setAwakeLocked(want)
}

func (t *Tracker) setAwakeLocked(on bool) {
	if on == t.awake {
		return
	}
	t.awake = on
	if t.keeper != nil {
		t.keeper.SetAwake(on)
	}
}

// Run 定时 Tick，直到 ctx 结束；结束时恢复睡眠。
func (t *Tracker) Run(ctx context.Context) {
	tk := time.NewTicker(tickInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Close()
			return
		case <-tk.C:
			t.Tick()
		}
	}
}

// Close 恢复睡眠（若仍在阻止）。
func (t *Tracker) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setAwakeLocked(false)
}
