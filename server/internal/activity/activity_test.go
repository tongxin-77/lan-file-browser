package activity

import (
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time              { return c.t }
func (c *fakeClock) add(d time.Duration)         { c.t = c.t.Add(d) }
func newClock() *fakeClock                       { return &fakeClock{time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)} }
func newTracker(c *fakeClock, k Keeper) *Tracker { return New(Options{Now: c.now, Keeper: k}) }

// fakeKeeper 记录 SetAwake 调用序列。
type fakeKeeper struct {
	mu    sync.Mutex
	calls []bool
}

func (k *fakeKeeper) SetAwake(on bool) { k.mu.Lock(); k.calls = append(k.calls, on); k.mu.Unlock() }
func (k *fakeKeeper) awake() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.calls) > 0 && k.calls[len(k.calls)-1]
}

func TestStreamCounting(t *testing.T) {
	c := newClock()
	tr := newTracker(c, nil)
	if tr.Active() {
		t.Fatal("初始不应活跃")
	}
	end1 := tr.StreamStart(`D:\`)
	end2 := tr.StreamStart(`D:\`)
	end3 := tr.StreamStart(`E:\`)
	if !tr.Active() || !tr.ActiveOn(`d:\`) || !tr.ActiveOn(`E:\`) || tr.ActiveOn(`F:\`) {
		t.Fatal("按卷判定错误")
	}
	if got := tr.ActiveVolumes(); !slices.Equal(got, []string{`D:\`, `E:\`}) {
		t.Fatalf("ActiveVolumes: %v", got)
	}
	end1()
	end1() // 重复调用无副作用
	if !tr.ActiveOn(`D:\`) {
		t.Fatal("D: 仍有一路流")
	}
	end2()
	if tr.ActiveOn(`D:\`) || !tr.Active() {
		t.Fatal("D: 应已结束，E: 仍活跃")
	}
	end3()
	if tr.Active() {
		t.Fatal("全部结束后不应活跃")
	}
}

// /api/progress 上报后 60 秒内视为活跃。
func TestProgressWindow(t *testing.T) {
	c := newClock()
	tr := newTracker(c, nil)
	tr.Progress(`D:\`)
	if !tr.Active() || !tr.ActiveOn(`D:\`) || tr.ActiveOn(`E:\`) {
		t.Fatal("进度上报后应活跃")
	}
	c.add(59 * time.Second)
	if !tr.ActiveOn(`D:\`) {
		t.Fatal("60 秒内仍应活跃")
	}
	c.add(2 * time.Second)
	if tr.Active() || len(tr.ActiveVolumes()) != 0 {
		t.Fatal("超过 60 秒不应活跃")
	}
}

// 有活跃播放即阻止睡眠；全部结束满 ResumeAfter（默认 5 分钟）后恢复；期间再播放则取消计时。
func TestSleepKeeperTiming(t *testing.T) {
	c := newClock()
	k := &fakeKeeper{}
	tr := newTracker(c, k)

	end := tr.StreamStart(`D:\`)
	if !k.awake() {
		t.Fatal("开始播放应立即阻止睡眠")
	}
	c.add(time.Hour)
	tr.Tick()
	end()
	c.add(4 * time.Minute)
	tr.Tick()
	if !k.awake() {
		t.Fatal("结束未满 5 分钟不应恢复")
	}
	end = tr.StreamStart(`E:\`) // 期间又播放：取消计时
	end()
	c.add(4 * time.Minute)
	tr.Tick()
	if !k.awake() {
		t.Fatal("重新播放后计时应从最后结束时刻算起")
	}
	c.add(time.Minute)
	tr.Tick()
	if k.awake() {
		t.Fatal("结束满 5 分钟应恢复睡眠")
	}
	// 只有进度上报：活跃至上报后 60 秒，再过 5 分钟恢复
	tr.Progress(`D:\`)
	if !k.awake() {
		t.Fatal("进度上报应阻止睡眠")
	}
	c.add(60*time.Second + 5*time.Minute - time.Second)
	tr.Tick()
	if !k.awake() {
		t.Fatal("未到恢复时刻")
	}
	c.add(time.Second)
	tr.Tick()
	if k.awake() {
		t.Fatal("应已恢复")
	}
	// 状态未变不重复调用 Keeper
	n := len(k.calls)
	tr.Tick()
	tr.Tick()
	if len(k.calls) != n {
		t.Fatalf("重复调用 SetAwake: %v", k.calls)
	}
	if !slices.Equal(k.calls, []bool{true, false, true, false}) {
		t.Fatalf("调用序列: %v", k.calls)
	}
}

func TestCustomResumeAfter(t *testing.T) {
	c := newClock()
	k := &fakeKeeper{}
	tr := New(Options{Now: c.now, Keeper: k, ResumeAfter: time.Minute})
	tr.StreamStart(`D:\`)()
	c.add(time.Minute)
	tr.Tick()
	if k.awake() {
		t.Fatal("ResumeAfter=1m 应已恢复")
	}
}

// Close 时若仍阻止睡眠则恢复。
func TestCloseReleases(t *testing.T) {
	c := newClock()
	k := &fakeKeeper{}
	tr := newTracker(c, k)
	tr.StreamStart(`D:\`)
	tr.Close()
	if k.awake() {
		t.Fatal("Close 应恢复睡眠")
	}
}

// 按文件记录活跃播放：视频流进行中或 60 秒内有进度上报；Playing 对文件本身或其所在目录（任一祖先）为真。
func TestPlayingFile(t *testing.T) {
	c := newClock()
	tr := newTracker(c, nil)
	end := tr.StreamFile(`D:\Anime\Show\E01.mkv`)
	if !tr.ActiveOn(VolumeOf(`D:\Anime\Show\E01.mkv`)) {
		t.Fatal("StreamFile 应同时计入卷的活跃播放")
	}
	for p, want := range map[string]bool{
		`d:\anime\show\e01.mkv`: true, `D:\Anime\Show`: true, `D:\Anime\`: true,
		`D:\Anime\Show\E02.mkv`: false, `D:\Anime\Sh`: false, `D:\Other`: false,
	} {
		if tr.Playing(p) != want {
			t.Errorf("流进行中 Playing(%s) = %v, want %v", p, !want, want)
		}
	}
	end()
	if tr.Playing(`D:\Anime\Show\E01.mkv`) {
		t.Fatal("流结束后不应仍在播放")
	}

	tr.ProgressFile(`E:\M\a.mp4`)
	if !tr.Playing(`E:\M\a.mp4`) || !tr.Playing(`E:\M`) || !tr.ActiveOn(VolumeOf(`E:\M\a.mp4`)) {
		t.Fatal("进度上报后应视为正在播放")
	}
	c.add(61 * time.Second)
	if tr.Playing(`E:\M\a.mp4`) {
		t.Fatal("超过 60 秒无上报不应视为正在播放")
	}
}
