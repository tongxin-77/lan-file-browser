package organize

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lfb/server/internal/config"
	"lfb/server/internal/store"
)

// fakeClock：Sleep 立即推进时间并累计，hook 可在每次睡眠时介入（如取消 ctx）。
type fakeClock struct {
	mu    sync.Mutex
	t     time.Time
	slept time.Duration
	hook  func(d time.Duration)
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.slept += d
	h := c.hook
	c.mu.Unlock()
	if h != nil {
		h(d)
	}
	return ctx.Err()
}
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fakeRecycler struct {
	err   error
	paths []string
}

func (r *fakeRecycler) Recycle(p string) error {
	if r.err != nil {
		return r.err
	}
	r.paths = append(r.paths, p)
	return os.Remove(p)
}

// fakeActivity：until 之前（按假时钟）在 vol 上有播放。
type fakeActivity struct {
	clock *fakeClock
	vol   string
	until time.Time
}

func (a *fakeActivity) ActiveVolumes() []string {
	if a.clock.Now().Before(a.until) {
		return []string{a.vol}
	}
	return nil
}

type env struct {
	t        *testing.T
	src, lib string
	cfg      *config.Store
	db       *sql.DB
	clock    *fakeClock
	rec      *fakeRecycler
	m        *Manager
	free     uint64
	opts     Options
}

func newEnv(t *testing.T, mutate func(o *config.Organize), opt func(o *Options)) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{t: t, src: filepath.Join(base, "dl"), lib: filepath.Join(base, "lib"),
		clock: &fakeClock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local)}, rec: &fakeRecycler{}, free: 1 << 40}
	os.MkdirAll(e.src, 0o755)
	os.MkdirAll(e.lib, 0o755)
	c, err := config.Load(filepath.Join(base, "data"))
	if err != nil {
		t.Fatal(err)
	}
	e.cfg = config.NewStore(c)
	zero := 0
	org := &config.Organize{Enabled: true, RateLimit: &zero,
		Sources: []config.OrganizeSource{{ID: "s1", Path: e.src, Mode: config.OrganizeCopy}}}
	if mutate != nil {
		mutate(org)
	}
	e.cfg.Update(func(c *config.Config) error {
		c.Shares = append(c.Shares, config.Share{ID: "sh", Name: "库", Path: e.lib, Organize: org})
		return nil
	})
	if e.db, err = store.Open(filepath.Join(base, "data")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.db.Close() })
	e.opts = Options{Config: e.cfg, DB: e.db, Clock: e.clock, Recycler: e.rec,
		SameVolume: func(a, b string) bool { return false },
		DiskOf:     func(p string) string { return "disk:" + strings.ToLower(filepath.VolumeName(p)) },
		FreeSpace:  func(string) (uint64, uint64, error) { return e.free, 1 << 41, nil }}
	if opt != nil {
		opt(&e.opts)
	}
	e.m = New(e.opts)
	return e
}

func (e *env) write(rel string, data []byte, mtime time.Time) {
	e.t.Helper()
	p := filepath.Join(e.src, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	if !mtime.IsZero() {
		os.Chtimes(p, mtime, mtime)
	}
}

// run 执行一次扫描；两次扫描之间推进 1 分钟。
func (e *env) run() {
	e.t.Helper()
	if err := e.m.RunOnce(context.Background(), "sh"); err != nil {
		e.t.Fatal(err)
	}
	e.clock.add(time.Minute)
}

func (e *env) libFiles() []string {
	var out []string
	filepath.WalkDir(e.lib, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			r, _ := filepath.Rel(e.lib, p)
			out = append(out, filepath.ToSlash(r))
		}
		return nil
	})
	return out
}

func (e *env) has(rel string) bool {
	_, err := os.Stat(filepath.Join(e.lib, filepath.FromSlash(rel)))
	return err == nil
}

func (e *env) items() []Item {
	items, _, err := ListItems(e.db, "sh", "", 0, 1000)
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

func data(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i*7)
	}
	return b
}

func TestOrganizeCopyFlow(t *testing.T) {
	e := newEnv(t, nil, nil)
	mt := time.Date(2025, 6, 1, 8, 0, 0, 0, time.Local)
	e.write("Show.S01E01.mkv", data(3<<20, 1), mt)
	e.write("Show.S01E01.chs.srt", []byte("1\n"), mt)
	e.write("orphan.srt", []byte("x"), mt)
	e.write("IMG_20250101_120000.jpg", jpegWithExif("2023:12:25 08:00:00", true), mt)
	e.write("dl.mkv", data(1000, 2), mt)
	e.write("dl.mkv.aria2", []byte("x"), mt)
	e.write("big.mkv.part", data(1000, 3), mt)
	e.write("notes.nfo", []byte("x"), mt)
	e.write(".hidden.mkv", []byte("x"), mt)

	e.run()
	if got := e.libFiles(); len(got) != 0 {
		t.Fatalf("首次扫描只观察，不应复制: %v", got)
	}
	if st, _ := e.m.Status("sh"); st.Counts.Watching != 3 {
		t.Fatalf("观察中应为 3: %+v", st.Counts)
	}
	e.run()
	want := []string{"剧集/Show/第1季/Show.S01E01.mkv", "剧集/Show/第1季/Show.S01E01.chs.srt", "照片/2023/12/IMG_20250101_120000.jpg"}
	for _, w := range want {
		if !e.has(w) {
			t.Errorf("缺少 %s；库内: %v", w, e.libFiles())
		}
	}
	if len(e.libFiles()) != len(want) {
		t.Fatalf("库内多出文件（下载中/非媒体/孤立字幕/临时文件不应处理）: %v", e.libFiles())
	}
	fi, _ := os.Stat(filepath.Join(e.lib, "剧集", "Show", "第1季", "Show.S01E01.mkv"))
	if !fi.ModTime().Equal(mt) {
		t.Errorf("应保留 mtime: %v", fi.ModTime())
	}
	if _, err := os.Stat(filepath.Join(e.src, "Show.S01E01.mkv")); err != nil {
		t.Error("复制模式应保留源文件")
	}
	for _, it := range e.items() {
		if it.State != StateDone || it.SourceAction != ActKept {
			t.Errorf("%s: %s %s", it.SrcPath, it.State, it.SourceAction)
		}
	}
	if items, _, _ := e.m.DryRun("sh"); len(items) != 4 {
		t.Fatalf("试运行: %+v", items)
	} else {
		for _, it := range items {
			if it.SrcPath != "dl.mkv" && it.Action != "done" {
				t.Errorf("已整理（含字幕）应显示 done: %+v", it)
			}
		}
	}
	// .aria2 删除后（下载完成）再整理
	os.Remove(filepath.Join(e.src, "dl.mkv.aria2"))
	e.run()
	e.run()
	if !e.has("电影/dl.mkv") {
		t.Errorf("下载完成后应整理: %v", e.libFiles())
	}
}

// 整理过的文件即使从片库删除也不再整理；同内容仅 mtime 变化也跳过。
func TestNoReorganize(t *testing.T) {
	e := newEnv(t, nil, nil)
	e.write("Movie.2020.mkv", data(5000, 1), time.Time{})
	e.run()
	e.run()
	target := filepath.Join(e.lib, "电影", "Movie (2020)", "Movie.2020.mkv")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	e.run()
	e.run()
	os.Chtimes(filepath.Join(e.src, "Movie.2020.mkv"), time.Now(), time.Now().Add(time.Hour))
	e.run()
	e.run()
	if _, err := os.Stat(target); err == nil {
		t.Fatal("已整理过的文件不应再次整理")
	}
	if n := len(e.items()); n != 1 {
		t.Fatalf("记录数 %d", n)
	}
	// 同路径新内容 → 新记录
	e.write("Movie.2020.mkv", data(6000, 9), time.Time{})
	e.run()
	e.run()
	if _, err := os.Stat(target); err != nil {
		t.Fatal("新内容应整理")
	}
}

// 目标同名同内容 → duplicate；同名不同内容 → 加 (1)，字幕沿用最终主名。
func TestDuplicateAndRename(t *testing.T) {
	e := newEnv(t, nil, nil)
	same, diff := data(4000, 1), data(4000, 2)
	os.MkdirAll(filepath.Join(e.lib, "电影"), 0o755)
	os.WriteFile(filepath.Join(e.lib, "电影", "a movie.mkv"), same, 0o644)
	os.WriteFile(filepath.Join(e.lib, "电影", "b movie.mkv"), same, 0o644)
	e.write("a movie.mkv", same, time.Time{})
	e.write("b movie.mkv", diff, time.Time{})
	e.write("b movie.zh-CN.ass", []byte("sub"), time.Time{})
	e.run()
	e.run()
	if !e.has("电影/b movie (1).mkv") || !e.has("电影/b movie (1).zh-CN.ass") || e.has("电影/a movie (1).mkv") {
		t.Fatalf("库内: %v", e.libFiles())
	}
	states := map[string]string{}
	for _, it := range e.items() {
		states[it.SrcPath] = it.State + " " + it.TargetPath
	}
	if states["a movie.mkv"] != "duplicate 电影/a movie.mkv" || states["b movie.mkv"] != "done 电影/b movie (1).mkv" {
		t.Fatalf("记录: %v", states)
	}
}

func TestMoveModes(t *testing.T) {
	for _, c := range []struct {
		name      string
		after     string
		recErr    error
		wantAct   string
		srcRemain bool
	}{
		{"recycle", config.AfterMoveRecycle, nil, ActRecycled, false},
		{"delete", config.AfterMoveDelete, nil, ActDeleted, false},
		{"recycle 不可用", config.AfterMoveRecycle, errRecycleBinUnsupported, ActRecycleFailed, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, func(o *config.Organize) { o.Sources[0].Mode, o.Sources[0].AfterMove = config.OrganizeMove, c.after }, nil)
			e.rec.err = c.recErr
			e.write("x movie.mp4", data(3000, 1), time.Time{})
			e.run()
			e.run()
			if !e.has("电影/x movie.mp4") {
				t.Fatalf("库内: %v", e.libFiles())
			}
			_, err := os.Stat(filepath.Join(e.src, "x movie.mp4"))
			if (err == nil) != c.srcRemain {
				t.Fatalf("源文件保留=%v，期望 %v", err == nil, c.srcRemain)
			}
			if it := e.items()[0]; it.SourceAction != c.wantAct {
				t.Fatalf("source_action %s", it.SourceAction)
			}
			if c.after == config.AfterMoveDelete && len(e.rec.paths) != 0 {
				t.Fatal("delete 模式不应进回收站")
			}
		})
	}
}

// 同一卷 + 移动：直接重命名，不复制。
func TestMoveSameVolumeRename(t *testing.T) {
	e := newEnv(t, func(o *config.Organize) { o.Sources[0].Mode = config.OrganizeMove }, func(o *Options) {
		o.SameVolume = func(a, b string) bool { return true }
		o.FreeSpace = func(string) (uint64, uint64, error) { return 0, 1, nil } // 空间不足也不影响重命名
	})
	mt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.Local)
	e.write("y movie.mp4", data(3000, 1), mt)
	e.run()
	e.run()
	fi, err := os.Stat(filepath.Join(e.lib, "电影", "y movie.mp4"))
	if err != nil || !fi.ModTime().Equal(mt) {
		t.Fatalf("重命名失败: %v %v", err, e.libFiles())
	}
	if it := e.items()[0]; it.SourceAction != ActRenamed || it.State != StateDone {
		t.Fatalf("%+v", it)
	}
}

// 仍在写入（其他进程持有写句柄）的文件不整理。
func TestLockedFileWaits(t *testing.T) {
	e := newEnv(t, nil, nil)
	e.write("w movie.mkv", data(3000, 1), time.Time{})
	f, err := os.OpenFile(filepath.Join(e.src, "w movie.mkv"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.run()
	e.run()
	if len(e.libFiles()) != 0 {
		t.Fatal("被写入占用的文件不应整理")
	}
	items, total, _ := e.m.DryRun("sh")
	if total != 1 || items[0].Action != "locked" {
		t.Fatalf("试运行: %+v", items)
	}
	f.Close()
	e.run()
	if !e.has("电影/w movie.mkv") {
		t.Fatal("释放后应整理")
	}
}

// 两次扫描间大小变化：继续观察。
func TestUnstableFileWaits(t *testing.T) {
	e := newEnv(t, nil, nil)
	e.write("g movie.mkv", data(1000, 1), time.Time{})
	e.run()
	e.write("g movie.mkv", data(2000, 1), time.Time{})
	e.run()
	if len(e.libFiles()) != 0 {
		t.Fatal("大小变化后不应整理")
	}
	e.run()
	if !e.has("电影/g movie.mkv") {
		t.Fatal("稳定后应整理")
	}
}

func TestLowSpacePauses(t *testing.T) {
	e := newEnv(t, nil, nil)
	e.free = 10 * gib // 默认 minFree：min(2 TiB*10%, 20 GB) = 20 GB
	e.write("s movie.mkv", data(1000, 1), time.Time{})
	e.run()
	e.run()
	if len(e.libFiles()) != 0 {
		t.Fatal("空间不足不应复制")
	}
	st, _ := e.m.Status("sh")
	if st.State != "paused" || st.PauseReason != "lowSpace" || st.MinFreeBytes != 20*gib {
		t.Fatalf("状态: %+v", st)
	}
	e.free = 30 * gib
	e.run()
	if !e.has("电影/s movie.mkv") {
		t.Fatal("空间恢复后应复制")
	}
}

func TestManualPause(t *testing.T) {
	e := newEnv(t, func(o *config.Organize) { o.Paused = true }, nil)
	e.write("p movie.mkv", data(1000, 1), time.Time{})
	e.run()
	e.run()
	if len(e.libFiles()) != 0 {
		t.Fatal("手动暂停不应复制")
	}
	if st, _ := e.m.Status("sh"); st.State != "paused" || st.PauseReason != "manual" || st.Counts.Queued != 1 {
		t.Fatalf("状态: %+v", st)
	}
}

// 播放暂停：同盘暂停至播放结束后 2 分钟；非同盘、pauseOnPlay=false 不暂停。
func TestPauseOnPlay(t *testing.T) {
	for _, c := range []struct {
		name      string
		vol       string
		onPlay    bool
		wantPause bool
	}{
		{"同盘（库盘）", `L:\`, true, true},
		{"同盘（源盘）", `S:\`, true, true},
		{"其他盘", `X:\`, true, false},
		{"关闭 pauseOnPlay", `L:\`, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var e *env
			act := &fakeActivity{vol: c.vol}
			e = newEnv(t, func(o *config.Organize) { o.PauseOnPlay = &c.onPlay }, func(o *Options) {
				o.Activity = act
				o.DiskOf = func(p string) string {
					switch {
					case strings.HasPrefix(p, e.lib), p == `L:\`:
						return "disk:1"
					case strings.HasPrefix(p, e.src), p == `S:\`:
						return "disk:2"
					}
					return "disk:9"
				}
			})
			act.clock = e.clock
			e.write("q movie.mkv", data(3<<20, 1), time.Time{})
			e.run()
			act.until = e.clock.Now().Add(30 * time.Second)
			start := e.clock.Now()
			e.run()
			if !e.has("电影/q movie.mkv") {
				t.Fatal("应最终复制完成")
			}
			paused := e.clock.slept > 0
			if paused != c.wantPause {
				t.Fatalf("暂停=%v，期望 %v", paused, c.wantPause)
			}
			if c.wantPause && e.clock.Now().Sub(start) < 30*time.Second+playCooldown {
				t.Fatalf("应在播放结束 2 分钟后才继续，实际 %v", e.clock.Now().Sub(start))
			}
		})
	}
}

// 复制中途播放：在块间停住，临时文件保留，继续后完成。
func TestPauseMidCopy(t *testing.T) {
	var e *env
	act := &fakeActivity{vol: `L:\`}
	e = newEnv(t, nil, func(o *Options) {
		o.Activity = act
		o.DiskOf = func(p string) string {
			if strings.HasPrefix(p, e.lib) || p == `L:\` {
				return "disk:1"
			}
			return "disk:2"
		}
	})
	act.clock = e.clock
	e.write("r movie.mkv", data(4<<20, 1), time.Time{})
	e.run()
	var sawTemp bool
	e.clock.hook = func(time.Duration) {
		for _, f := range e.libFiles() {
			if strings.Contains(f, tempPrefix) {
				sawTemp = true
			}
		}
	}
	calls := 0
	e.m.o.Activity = activityFunc(func() []string {
		calls++
		if calls == 3 { // 第 3 块前开始播放
			act.until = e.clock.Now().Add(10 * time.Second)
		}
		return act.ActiveVolumes()
	})
	e.run()
	if !sawTemp || !e.has("电影/r movie.mkv") {
		t.Fatalf("暂停时应保留临时文件并最终完成: sawTemp=%v %v", sawTemp, e.libFiles())
	}
}

type activityFunc func() []string

func (f activityFunc) ActiveVolumes() []string { return f() }

// 退出：当前块后停止，删除临时文件，记录回到排队。
func TestShutdownCleansTemp(t *testing.T) {
	five := 1
	e := newEnv(t, func(o *config.Organize) { o.RateLimit = &five }, nil)
	e.write("z movie.mkv", data(4<<20, 1), time.Time{})
	e.run()
	ctx, cancel := context.WithCancel(context.Background())
	e.clock.hook = func(time.Duration) { cancel() }
	if err := e.m.RunOnce(ctx, "sh"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if files := e.libFiles(); len(files) != 0 {
		t.Fatalf("应清理临时文件: %v", files)
	}
	if it := e.items()[0]; it.State != StateQueued || it.Attempts != 0 {
		t.Fatalf("%+v", it)
	}
}

func TestStartupResetsAndCleans(t *testing.T) {
	e := newEnv(t, nil, nil)
	os.MkdirAll(filepath.Join(e.lib, "电影"), 0o755)
	tmp := filepath.Join(e.lib, "电影", tempPrefix+"abc")
	os.WriteFile(tmp, []byte("x"), 0o644)
	it := &Item{ShareID: "sh", SourceID: "s1", SrcPath: "a.mkv", SrcKey: "a.mkv", Fingerprint: "1:x", Kind: KindVideo,
		Category: CatMovie, TargetPath: "电影/a.mkv", State: StateCopying}
	insertItem(e.db, it, time.Now())
	e.m.Startup()
	if _, err := os.Stat(tmp); err == nil {
		t.Fatal("残留临时文件应清理")
	}
	if got := e.items()[0].State; got != StateQueued {
		t.Fatalf("state %s", got)
	}
}

// 试运行不复制、不写记录，也不影响观察状态。
func TestDryRunNoSideEffects(t *testing.T) {
	e := newEnv(t, nil, nil)
	e.write("Show.S01E02.mkv", data(1000, 1), time.Time{})
	e.write("Show.S01E02.chs.srt", []byte("s"), time.Time{})
	e.write("half.mkv", data(10, 1), time.Time{})
	e.write("half.mkv.aria2", data(10, 1), time.Time{})
	items, total, err := e.m.DryRun("sh")
	if err != nil || total != 3 {
		t.Fatalf("%v %d %+v", err, total, items)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.SrcPath] = it.Action + " " + it.TargetPath
	}
	if got["Show.S01E02.mkv"] != "copy 剧集/Show/第1季/Show.S01E02.mkv" || got["Show.S01E02.chs.srt"] != "copy 剧集/Show/第1季/Show.S01E02.chs.srt" ||
		!strings.HasPrefix(got["half.mkv"], "waiting ") {
		t.Fatalf("%v", got)
	}
	if len(e.libFiles()) != 0 || len(e.items()) != 0 {
		t.Fatal("试运行不应产生文件或记录")
	}
	e.run() // 试运行不算作一次观察：这次仍只观察
	if len(e.libFiles()) != 0 {
		t.Fatal("试运行不应计入观察")
	}
}

func TestCopyVerifyFailCleansTemp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.bin")
	os.WriteFile(src, data(3<<20, 1), 0o644)
	lib := filepath.Join(dir, "lib")
	os.Mkdir(lib, 0o755)
	root, _ := os.OpenRoot(lib)
	defer root.Close()
	f, _ := os.Open(src)
	defer f.Close()
	_, err := copyToTemp(context.Background(), copyJob{src: f, size: 3 << 20, fp: "bogus", mtime: time.Now(), root: root,
		gate: func(context.Context) error { return nil }})
	if err == nil {
		t.Fatal("指纹不一致应失败")
	}
	entries, _ := os.ReadDir(lib)
	if len(entries) != 0 {
		t.Fatalf("应删除临时文件: %v", entries)
	}
}

func TestRateLimit(t *testing.T) {
	c := &fakeClock{t: time.Unix(0, 0)}
	lim := &limiter{rate: 2 * bytesPerMiB}
	for range 10 { // 10 MiB @ 2 MiB/s ≈ 5 s
		if err := lim.wait(context.Background(), c, chunkSize); err != nil {
			t.Fatal(err)
		}
	}
	if c.slept < 4900*time.Millisecond || c.slept > 5100*time.Millisecond {
		t.Fatalf("限速睡眠 %v", c.slept)
	}
	unl := &limiter{}
	c.slept = 0
	unl.wait(context.Background(), c, chunkSize)
	if c.slept != 0 {
		t.Fatal("不限速不应睡眠")
	}
}

func TestFingerprint(t *testing.T) {
	small, big := data(100, 1), data(3<<20, 1)
	a, _ := fingerprint(bytes.NewReader(small), 100)
	if !strings.HasPrefix(a, "100:") {
		t.Fatal(a)
	}
	b1, _ := fingerprint(bytes.NewReader(big), int64(len(big)))
	big[len(big)/2] ^= 0xFF // 中间字节不参与快速指纹
	b2, _ := fingerprint(bytes.NewReader(big), int64(len(big)))
	big[len(big)-1] ^= 0xFF
	b3, _ := fingerprint(bytes.NewReader(big), int64(len(big)))
	if b1 != b2 || b1 == b3 {
		t.Fatal("快速指纹应只取首尾 1 MiB")
	}
}

func mklinkJ(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J 失败: %v %s", err, out)
	}
}

func TestCheckLayout(t *testing.T) {
	base := t.TempDir()
	d := func(p string) string { p = filepath.Join(base, p); os.MkdirAll(p, 0o755); return p }
	lib, dl, other, lib2 := d("lib"), d("dl"), d("other"), d("lib2")
	mklinkJ(t, filepath.Join(base, "j"), filepath.Join(lib, "sub"))
	d("lib/sub")
	src := func(ps ...string) *config.Organize {
		o := &config.Organize{}
		for i, p := range ps {
			o.Sources = append(o.Sources, config.OrganizeSource{ID: string(rune('a' + i)), Path: p, Mode: "copy"})
		}
		return o
	}
	cases := []struct {
		name     string
		shares   []config.Share
		bad      bool
		conflict bool
	}{
		{"正常", []config.Share{{ID: "1", Path: lib, Organize: src(dl)}}, false, false},
		{"源=库", []config.Share{{ID: "1", Path: lib, Organize: src(strings.ToUpper(lib))}}, true, false},
		{"源在库内", []config.Share{{ID: "1", Path: lib, Organize: src(d("lib/x"))}}, true, false},
		{"库在源内", []config.Share{{ID: "1", Path: d("dl/lib"), Organize: src(dl)}}, true, false},
		{"联接解析后在库内", []config.Share{{ID: "1", Path: lib, Organize: src(filepath.Join(base, "j"))}}, true, false},
		{"前缀非边界", []config.Share{{ID: "1", Path: lib, Organize: src(d("lib-2"))}}, false, false},
		{"源重叠", []config.Share{{ID: "1", Path: lib, Organize: src(dl, d("dl/a"))}}, true, false},
		{"跨片库：库在另一片库源内", []config.Share{{ID: "1", Path: lib, Organize: src(dl)}, {ID: "2", Path: lib2, Organize: src(lib)}}, true, false},
		{"跨片库：源重叠", []config.Share{{ID: "1", Path: lib, Organize: src(dl)}, {ID: "2", Path: lib2, Organize: src(d("dl/b"))}}, true, true},
		{"未配置整理的普通共享不参与", []config.Share{{ID: "1", Path: lib, Organize: src(dl)}, {ID: "2", Path: base}}, false, false},
		{"不同源", []config.Share{{ID: "1", Path: lib, Organize: src(dl)}, {ID: "2", Path: lib2, Organize: src(other)}}, false, false},
	}
	for _, c := range cases {
		err := CheckLayout(&config.Config{Shares: c.shares})
		var le *LayoutError
		if (err != nil) != c.bad || err != nil && (!errors.As(err, &le) || le.Conflict != c.conflict) {
			t.Errorf("%s: %v", c.name, err)
		}
		if err != nil && strings.Contains(err.Error(), base) {
			t.Errorf("%s: 错误信息不应含路径", c.name)
		}
	}
}

// 源文件夹中的目录联接不跟随。
func TestScanSkipsLinks(t *testing.T) {
	base := t.TempDir()
	src, outside := filepath.Join(base, "src"), filepath.Join(base, "outside")
	os.MkdirAll(src, 0o755)
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "secret.mkv"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(src, "ok.mkv"), []byte("x"), 0o644)
	mklinkJ(t, filepath.Join(src, "link"), outside)
	es, err := scanSource(src)
	if err != nil || len(es) != 1 || es[0].Rel != "ok.mkv" {
		t.Fatalf("%v %+v", err, es)
	}
}

// 真实回收站：会在当前用户回收站留下一个测试文件，默认跳过；LFB_TEST_RECYCLE=1 时运行。
func TestShellRecyclerReal(t *testing.T) {
	if os.Getenv("LFB_TEST_RECYCLE") != "1" {
		t.Skip("设置 LFB_TEST_RECYCLE=1 运行")
	}
	p := filepath.Join(t.TempDir(), "lfb-organize-recycle-test.txt")
	os.WriteFile(p, []byte("x"), 0o644)
	if err := (ShellRecycler{}).Recycle(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("文件应已移入回收站")
	}
}

// 路径尚不存在时按最近的已存在祖先解析（联接、8.3 短名），不能退化为原样比较。
func TestNestedMissingUnderJunction(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "lib")
	os.MkdirAll(lib, 0o755)
	mklinkJ(t, filepath.Join(base, "j"), lib)
	if !Nested(lib, filepath.Join(base, "j", "x", "y")) {
		t.Fatal("联接下不存在的子路径应判为嵌套")
	}
	if Nested(filepath.Join(base, "j", "x"), filepath.Join(base, "other")) {
		t.Fatal("不相关路径不应判为嵌套")
	}
}

func TestDiskKeyAndCheckPath(t *testing.T) {
	dir := t.TempDir()
	k := diskKey(dir)
	if !strings.HasPrefix(k, "disk:") && !strings.HasPrefix(k, "vol:") {
		t.Fatal(k)
	}
	m := New(Options{})
	c := m.CheckPath(dir, filepath.Join(dir, "x"))
	if !c.SameDisk || !c.SameVolume || !c.Nested {
		t.Fatalf("%+v", c)
	}
	if !sameDisk("disk:1,2", "disk:2") || sameDisk("disk:1", "disk:2") || sameDisk("vol:a", "disk:1") {
		t.Fatal("sameDisk")
	}
}

// 保持源文件夹结构：相对源文件夹根的路径原样保留，不按类型/剧名归类；重名、字幕跟随照旧。
func TestKeepStructure(t *testing.T) {
	e := newEnv(t, func(o *config.Organize) { o.Sources[0].KeepStructure = true }, nil)
	mt := time.Date(2025, 6, 1, 8, 0, 0, 0, time.Local)
	e.write("A/B/C/Show.S01E01.mkv", data(3000, 1), mt)
	e.write("A/B/C/Show.S01E01.chs.srt", []byte("1\n"), mt)
	e.write("A/IMG_20250101_120000.jpg", data(500, 2), mt)
	e.write("root.mp4", data(800, 3), mt)
	e.write("A/.hidden/x.mkv", data(10, 4), mt)
	os.WriteFile(filepath.Join(e.lib, "root.mp4"), data(900, 5), 0o644) // 片库里已有同名不同内容

	items, total, err := e.m.DryRun("sh")
	if err != nil || total != 4 {
		t.Fatalf("试运行: %v %d %+v", err, total, items)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.SrcPath] = it.Category + " " + it.Action + " " + it.TargetPath
	}
	want := map[string]string{
		"A/B/C/Show.S01E01.mkv":     "kept copy A/B/C/Show.S01E01.mkv",
		"A/B/C/Show.S01E01.chs.srt": "kept copy A/B/C/Show.S01E01.chs.srt",
		"A/IMG_20250101_120000.jpg": "kept copy A/IMG_20250101_120000.jpg",
		"root.mp4":                  "kept copy root.mp4",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("试运行 %s: %q，应为 %q", k, got[k], v)
		}
	}

	e.run()
	e.run()
	for _, w := range []string{"A/B/C/Show.S01E01.mkv", "A/B/C/Show.S01E01.chs.srt", "A/IMG_20250101_120000.jpg", "root (1).mp4"} {
		if !e.has(w) {
			t.Errorf("缺少 %s；库内: %v", w, e.libFiles())
		}
	}
	if n := len(e.libFiles()); n != 5 {
		t.Fatalf("库内文件数 %d: %v", n, e.libFiles())
	}
	for _, it := range e.items() {
		if it.State != StateDone || it.Category != CatKept {
			t.Errorf("%s: %s %s", it.SrcPath, it.State, it.Category)
		}
	}
}

// 关闭“保持源文件夹结构”时，嵌套目录中的文件照旧按类型/剧名归类。
func TestKeepStructureOffClassifies(t *testing.T) {
	e := newEnv(t, nil, nil)
	e.write("A/B/C/Show.S01E01.mkv", data(3000, 1), time.Time{})
	e.run()
	e.run()
	if got := e.libFiles(); len(got) != 1 || got[0] != "剧集/Show/第1季/Show.S01E01.mkv" {
		t.Fatalf("库内: %v", got)
	}
}

// 相对路径各段须通过 fsx 词法校验，否则不整理。
func TestKeepPlanValidates(t *testing.T) {
	if p, ok := keepPlan("A/B/x.mp4"); !ok || p.Target() != "A/B/x.mp4" || p.Category != CatKept {
		t.Fatalf("%+v %v", p, ok)
	}
	if p, ok := keepPlan("x.mp4"); !ok || p.Target() != "x.mp4" {
		t.Fatalf("%+v %v", p, ok)
	}
	for _, bad := range []string{"../x.mp4", "A/CON/x.mp4", "A/b./x.mp4", "A//x.mp4", "/x.mp4", "A/a:b/x.mp4"} {
		if _, ok := keepPlan(bad); ok {
			t.Errorf("%q 应拒绝", bad)
		}
	}
}

// 保持结构的源可能在片库任意目录留下临时文件：启动时清理整个库目录。
func TestStartupCleansKeepStructure(t *testing.T) {
	e := newEnv(t, func(o *config.Organize) { o.Sources[0].KeepStructure = true }, nil)
	os.MkdirAll(filepath.Join(e.lib, "A", "B"), 0o755)
	tmps := []string{filepath.Join(e.lib, tempPrefix+"r"), filepath.Join(e.lib, "A", "B", tempPrefix+"x")}
	for _, p := range tmps {
		os.WriteFile(p, []byte("x"), 0o644)
	}
	e.m.Startup()
	for _, p := range tmps {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("残留临时文件应清理: %s", p)
		}
	}
}
