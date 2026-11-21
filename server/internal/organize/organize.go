// Package organize 实现片库的自动整理（06 文档）：扫描源文件夹中下载完成的视频/图片，
// 按规则归类后复制或移动到库目录。
//
// 每个片库一个串行的扫描+复制过程；不同片库可并行扫描，但全局同一时刻只复制一个文件。
// 观察状态（两次扫描间的大小/mtime）只在内存；整理记录在 data.db 的 organize_items 表。
package organize

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lfb/server/internal/config"
)

const (
	minStableGap    = time.Minute     // 两次比对至少间隔（06 文档 4.2 #2）
	playCooldown    = 2 * time.Minute // 相关播放结束后多久继续
	pausePoll       = time.Second     // 暂停时复查间隔
	defaultFirstRun = time.Minute     // 启动后首扫
	schedulerTick   = 5 * time.Second
	gib             = 1 << 30
)

// Clock 提供时间与可取消的睡眠；测试注入假时钟。
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Activity 提供正在播放的卷（activity.Tracker 实现）。
type Activity interface {
	ActiveVolumes() []string
}

// Options 中除 Config、DB 外均可为零值（使用 Windows 实现）。
type Options struct {
	Config     *config.Store
	DB         *sql.DB
	Activity   Activity
	Clock      Clock
	Recycler   Recycler
	DiskOf     func(path string) string // 物理盘标识，默认 diskKey（按卷缓存）
	SameVolume func(a, b string) bool   // 是否同一卷（可直接重命名）
	FreeSpace  func(path string) (free, total uint64, err error)
	FirstRun   time.Duration // 启动后首扫延迟，默认 1 分钟
}

// Manager 调度各片库的整理。
type Manager struct {
	o      Options
	copyMu sync.Mutex // 全局单路复制
	wake   chan struct{}

	mu    sync.Mutex
	rts   map[string]*shareRT
	disks map[string]string // 卷挂载点 → 磁盘标识
	wg    sync.WaitGroup
}

// shareRT 为片库的运行时状态。
type shareRT struct {
	mu         sync.Mutex
	state      string // idle scanning copying paused offline
	reason     string // 运行中的暂停原因：lowSpace / playback（manual 由配置决定）
	errMsg     string
	lastScan   time.Time
	nextScan   time.Time
	scanReq    bool
	running    bool
	watching   int
	obs        map[string]obsRec
	lastPlay   time.Time
	cur        *Current
	free, need uint64
}

type obsRec struct {
	size  int64
	mtime time.Time
}

// Current 为正在复制的文件。
type Current struct {
	Name  string  `json:"name"`
	Bytes int64   `json:"bytes"`
	Total int64   `json:"total"`
	Speed float64 `json:"speed"` // 字节/秒
	start time.Time
}

// New 创建 Manager。
func New(o Options) *Manager {
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Recycler == nil {
		o.Recycler = ShellRecycler{}
	}
	if o.SameVolume == nil {
		o.SameVolume = func(a, b string) bool { return volumeName(a) == volumeName(b) }
	}
	if o.FreeSpace == nil {
		o.FreeSpace = freeSpace
	}
	if o.FirstRun <= 0 {
		o.FirstRun = defaultFirstRun
	}
	m := &Manager{o: o, wake: make(chan struct{}, 1), rts: map[string]*shareRT{}, disks: map[string]string{}}
	if m.o.DiskOf == nil {
		m.o.DiskOf = m.cachedDisk
	}
	return m
}

func (m *Manager) cachedDisk(p string) string {
	vol := strings.ToLower(volumeMount(p))
	m.mu.Lock()
	k, ok := m.disks[vol]
	m.mu.Unlock()
	if !ok {
		k = diskKey(vol)
		m.mu.Lock()
		m.disks[vol] = k
		m.mu.Unlock()
	}
	return k
}

func (m *Manager) rt(id string) *shareRT {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rts[id]
	if r == nil {
		r = &shareRT{state: "idle", obs: map[string]obsRec{}}
		m.rts[id] = r
	}
	return r
}

// params 为解析默认值后的片库整理参数。
type params struct {
	share       config.Share
	org         config.Organize
	interval    time.Duration
	rate        float64 // 字节/秒
	minFreeGB   *int
	maxRetries  int
	pauseOnPlay bool
}

func resolve(cfg *config.Config, id string) (params, bool) {
	sh := cfg.Share(id)
	if sh == nil || sh.Organize == nil {
		return params{}, false
	}
	d, o := cfg.OrganizeDefaults, *sh.Organize
	p := params{share: *sh, org: o, interval: time.Duration(d.Interval) * time.Minute, rate: float64(d.RateLimit) * bytesPerMiB,
		minFreeGB: d.MinFree, maxRetries: d.MaxRetries, pauseOnPlay: o.PauseOnPlay == nil || *o.PauseOnPlay}
	if o.Interval != nil {
		p.interval = time.Duration(*o.Interval) * time.Minute
	}
	if o.RateLimit != nil {
		p.rate = float64(*o.RateLimit) * bytesPerMiB
	}
	if o.MinFree != nil {
		p.minFreeGB = o.MinFree
	}
	if o.MaxRetries != nil {
		p.maxRetries = *o.MaxRetries
	}
	if p.interval < time.Minute {
		p.interval = time.Minute
	}
	return p, true
}

// minFreeBytes：null 时取“总容量 10% 与 20 GB 的较小值”。
func (p params) minFreeBytes(total uint64) uint64 {
	if p.minFreeGB != nil {
		return uint64(max(*p.minFreeGB, 0)) * gib
	}
	return min(total/10, 20*gib)
}

// ---------- 调度 ----------

// Run 启动调度，直到 ctx 结束；返回前等待所有片库的当前块写完并清理临时文件。
func (m *Manager) Run(ctx context.Context) {
	m.Startup()
	started := m.o.Clock.Now()
	for {
		now := m.o.Clock.Now()
		for _, sh := range m.o.Config.Get().Shares {
			if sh.Organize == nil || !sh.Organize.Enabled {
				continue
			}
			r := m.rt(sh.ID)
			r.mu.Lock()
			due := r.scanReq || (r.nextScan.IsZero() && now.Sub(started) >= m.o.FirstRun) || (!r.nextScan.IsZero() && !now.Before(r.nextScan))
			if due && !r.running {
				r.running, r.scanReq = true, false
				id := sh.ID
				m.wg.Add(1)
				go func() {
					defer m.wg.Done()
					m.runShare(ctx, id)
					r.mu.Lock()
					r.running = false
					r.mu.Unlock()
				}()
			}
			r.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			m.wg.Wait()
			return
		case <-m.wake:
		case <-time.After(schedulerTick):
		}
	}
}

// ScanNow 请求立即扫描（管理页“立即扫描”）。
func (m *Manager) ScanNow(id string) {
	r := m.rt(id)
	r.mu.Lock()
	r.scanReq = true
	r.mu.Unlock()
	m.Wake()
}

// Wake 让调度器立即复查（配置变化后调用）。
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) runShare(ctx context.Context, id string) {
	r := m.rt(id)
	r.mu.Lock()
	wait := minStableGap - m.o.Clock.Now().Sub(r.lastScan)
	if wait > 0 {
		r.state = "scanning"
	}
	r.mu.Unlock()
	if wait > 0 { // 与上次扫描不足 1 分钟：等满再比对
		if m.o.Clock.Sleep(ctx, wait) != nil {
			return
		}
	}
	if err := m.RunOnce(ctx, id); err != nil && ctx.Err() == nil {
		log.Printf("整理片库 %s 出错: %v", id, err) // 不记文件名（加密片库，06 文档第 9 节）
	}
}

// Startup 把未完成的 copying 记录改回 queued，并清理各库目录中残留的临时文件（06 文档第 6 节）。
func (m *Manager) Startup() {
	if err := resetCopying(m.o.DB); err != nil {
		log.Printf("整理记录复位失败: %v", err)
	}
	for _, sh := range m.o.Config.Get().Shares {
		if sh.Organize == nil {
			continue
		}
		tops := []string{"照片", "剧集", "电影", "未分类"}
		for _, src := range sh.Organize.Sources {
			if src.KeepStructure { // 保持结构时临时文件可能在库目录任意位置
				tops = []string{""}
			}
		}
		for _, top := range tops {
			filepath.WalkDir(filepath.Join(sh.Path, top), func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), tempPrefix) {
					os.Remove(p)
				}
				return nil
			})
		}
	}
}

// ---------- 一次扫描 + 复制 ----------

// RunOnce 对片库执行一次扫描与复制（同步）。
func (m *Manager) RunOnce(ctx context.Context, id string) error {
	p, ok := resolve(m.o.Config.Get(), id)
	if !ok {
		return nil
	}
	r := m.rt(id)
	now := m.o.Clock.Now()
	setState := func(s string) { r.mu.Lock(); r.state = s; r.mu.Unlock() }
	defer func() {
		r.mu.Lock()
		r.lastScan = m.o.Clock.Now()
		r.nextScan = r.lastScan.Add(p.interval)
		if r.state != "offline" {
			r.state = "idle"
		}
		r.cur = nil
		r.mu.Unlock()
	}()
	if fi, err := os.Stat(p.share.Path); err != nil || !fi.IsDir() {
		setState("offline")
		return nil
	}
	if err := CheckLayout(m.o.Config.Get()); err != nil { // 运行时再校验（片库路径可能被改动）
		r.mu.Lock()
		r.errMsg = err.Error()
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	r.state, r.errMsg = "scanning", ""
	r.mu.Unlock()

	obs := map[string]obsRec{}
	watching := 0
	var subs []srcEntry
	for _, src := range p.org.Sources {
		entries, err := scanSource(src.Path)
		if err != nil {
			continue // 源文件夹不存在：只跳过该源
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e.Kind == KindSubtitle {
				subs = append(subs, srcEntry{src, e})
				continue
			}
			st, err := m.observe(r, obs, p, src, e, now)
			if err != nil {
				return err
			}
			if st == "waiting" || st == "locked" {
				watching++
			}
		}
	}
	r.mu.Lock()
	r.watching = watching
	r.mu.Unlock()

	if err := m.copyPending(ctx, r, p); err != nil {
		return err
	}
	// 字幕跟随：视频整理完成后再处理（06 文档 5.3）
	for _, s := range subs {
		if err := m.observeSubtitle(r, obs, p, s.src, s.e, now); err != nil {
			return err
		}
	}
	if err := m.copyPending(ctx, r, p); err != nil {
		return err
	}
	r.mu.Lock()
	r.obs = obs
	r.mu.Unlock()
	return nil
}

type srcEntry struct {
	src config.OrganizeSource
	e   entry
}

func srcAbs(src config.OrganizeSource, rel string) string {
	return filepath.Join(src.Path, filepath.FromSlash(rel))
}

// readiness 判定下载是否完成（06 文档 4.2）：返回 ""（就绪）/ "waiting" / "locked"。会写入本次观察 obs。
func (m *Manager) readiness(r *shareRT, obs map[string]obsRec, src config.OrganizeSource, e entry) string {
	key := src.ID + "\x00" + strings.ToLower(e.Rel)
	obs[key] = obsRec{e.Size, e.Mtime}
	if e.Partial {
		return "waiting"
	}
	r.mu.Lock()
	prev, seen := r.obs[key]
	r.mu.Unlock()
	if !seen || prev.size != e.Size || !prev.mtime.Equal(e.Mtime) {
		return "waiting"
	}
	return ""
}

// observe 处理一个媒体文件：已整理的跳过；新文件稳定且可独占打开后计算指纹、归类、写入排队记录。
func (m *Manager) observe(r *shareRT, obs map[string]obsRec, p params, src config.OrganizeSource, e entry, now time.Time) (string, error) {
	key := strings.ToLower(e.Rel)
	rec, err := latestBySrc(m.o.DB, p.share.ID, src.ID, key)
	if err != nil {
		return "", err
	}
	if rec != nil && rec.Size == e.Size && rec.Mtime == e.Mtime.UnixNano() {
		return "known", nil // 快速跳过：不重新读文件
	}
	if st := m.readiness(r, obs, src, e); st != "" {
		return st, nil
	}
	f, err := openSourceRead(srcAbs(src, e.Rel))
	if err != nil {
		if errors.Is(err, errLocked) {
			return "locked", nil
		}
		return "skip", nil
	}
	defer f.Close()
	fp, err := fingerprint(f, e.Size)
	if err != nil {
		return "skip", nil
	}
	if old, err := byFingerprint(m.o.DB, p.share.ID, src.ID, key, fp); err != nil {
		return "", err
	} else if old != nil { // 同内容：更新 mtime 后跳过（即使片库里的文件已被删除）
		old.Size, old.Mtime, old.SrcPath = e.Size, e.Mtime.UnixNano(), e.Rel
		return "known", saveItem(m.o.DB, old, now)
	}
	plan, ok := planFor(src, e, photoTimeFunc(f, e.Rel))
	if !ok {
		return "skip", nil
	}
	it := &Item{ShareID: p.share.ID, SourceID: src.ID, SrcPath: e.Rel, SrcKey: key, Size: e.Size, Mtime: e.Mtime.UnixNano(),
		Fingerprint: fp, Kind: e.Kind, Category: plan.Category, TargetPath: plan.Target(), State: StateQueued}
	return "queued", insertItem(m.o.DB, it, now)
}

func photoTimeFunc(f *os.File, name string) func() (time.Time, bool) {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return func() (time.Time, bool) {
			if _, err := f.Seek(0, 0); err != nil {
				return time.Time{}, false
			}
			return exifDateTime(bufio.NewReader(f))
		}
	}
	return nil
}

// subtitleTarget 找同目录同主名、已整理的视频，返回字幕目标（沿用视频最终主名）与类别。
func subtitleTarget(videos []Item, rel string) (target, category string, ok bool) {
	dir, name := path.Split(strings.ToLower(rel))
	for _, v := range videos {
		vdir, vname := path.Split(v.SrcKey)
		stem := strings.TrimSuffix(vname, path.Ext(vname))
		if vdir != dir || !strings.HasPrefix(name, stem+".") {
			continue
		}
		suffix := path.Base(rel)[len(stem):] // 如 .chs.srt（保留原大小写）
		tdir, tname := path.Split(v.TargetPath)
		return tdir + strings.TrimSuffix(tname, path.Ext(tname)) + suffix, v.Category, true
	}
	return "", "", false
}

func (m *Manager) observeSubtitle(r *shareRT, obs map[string]obsRec, p params, src config.OrganizeSource, e entry, now time.Time) error {
	key := strings.ToLower(e.Rel)
	rec, err := latestBySrc(m.o.DB, p.share.ID, src.ID, key)
	if err != nil || rec != nil && rec.Size == e.Size && rec.Mtime == e.Mtime.UnixNano() {
		return err
	}
	if m.readiness(r, obs, src, e) != "" {
		return nil
	}
	videos, err := doneVideos(m.o.DB, p.share.ID, src.ID)
	if err != nil {
		return err
	}
	target, cat, ok := subtitleTarget(videos, e.Rel)
	if !ok {
		return nil // 孤立字幕不处理
	}
	f, err := openSourceRead(srcAbs(src, e.Rel))
	if err != nil {
		return nil
	}
	defer f.Close()
	fp, err := fingerprint(f, e.Size)
	if err != nil {
		return nil
	}
	if old, err := byFingerprint(m.o.DB, p.share.ID, src.ID, key, fp); err != nil || old != nil {
		if old != nil {
			old.Size, old.Mtime = e.Size, e.Mtime.UnixNano()
			return saveItem(m.o.DB, old, now)
		}
		return err
	}
	it := &Item{ShareID: p.share.ID, SourceID: src.ID, SrcPath: e.Rel, SrcKey: key, Size: e.Size, Mtime: e.Mtime.UnixNano(),
		Fingerprint: fp, Kind: KindSubtitle, Category: cat, TargetPath: target, State: StateQueued}
	return insertItem(m.o.DB, it, now)
}

// ---------- 复制 ----------

var errLowSpace = errors.New("库盘空间不足")

func (m *Manager) copyPending(ctx context.Context, r *shareRT, p params) error {
	items, err := pendingItems(m.o.DB, p.share.ID, p.maxRetries)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.reason = ""
	r.mu.Unlock()
	for i := range items {
		if m.manualPaused(p.share.ID) {
			return nil // 手动暂停：不开始新文件
		}
		src, ok := sourceByID(p, items[i].SourceID)
		if !ok {
			continue // 源已从配置中删除
		}
		if err := m.waitPlayback(ctx, r, p, src); err != nil {
			return err
		}
		err := m.copyItem(ctx, r, p, src, &items[i])
		if errors.Is(err, errLowSpace) {
			return nil // 该片库本次停住，下次扫描复查
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func sourceByID(p params, id string) (config.OrganizeSource, bool) {
	for _, s := range p.org.Sources {
		if s.ID == id {
			return s, true
		}
	}
	return config.OrganizeSource{}, false
}

func (m *Manager) manualPaused(id string) bool {
	sh := m.o.Config.Get().Share(id)
	return sh != nil && sh.Organize != nil && sh.Organize.Paused
}

// playing 报告是否有与源盘或库盘同一物理盘的活跃播放（含结束后 2 分钟）。
func (m *Manager) playing(r *shareRT, p params, src config.OrganizeSource) bool {
	if !p.pauseOnPlay || m.o.Activity == nil {
		return false
	}
	now := m.o.Clock.Now()
	vols := m.o.Activity.ActiveVolumes()
	if len(vols) > 0 {
		sd, ld := m.o.DiskOf(src.Path), m.o.DiskOf(p.share.Path)
		for _, v := range vols {
			if d := m.o.DiskOf(v); sameDisk(d, sd) || sameDisk(d, ld) {
				r.mu.Lock()
				r.lastPlay = now
				r.mu.Unlock()
				break
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.lastPlay.IsZero() && now.Sub(r.lastPlay) < playCooldown
}

func (m *Manager) setPause(r *shareRT, reason string) {
	r.mu.Lock()
	r.reason = reason
	r.mu.Unlock()
}

func (m *Manager) waitPlayback(ctx context.Context, r *shareRT, p params, src config.OrganizeSource) error {
	for m.playing(r, p, src) {
		m.setPause(r, "playback")
		if err := m.o.Clock.Sleep(ctx, pausePoll); err != nil {
			return err
		}
	}
	m.setPause(r, "")
	return nil
}

// gate 在块间检查手动暂停与同盘播放（停在当前块之后，临时文件与句柄保留），然后限速。
func (m *Manager) gate(r *shareRT, p params, src config.OrganizeSource, lim *limiter) func(context.Context) error {
	return func(ctx context.Context) error {
		paused := false
		for {
			reason := ""
			switch {
			case m.manualPaused(p.share.ID):
				reason = "manual"
			case m.playing(r, p, src):
				reason = "playback"
			}
			if reason == "" {
				break
			}
			paused = true
			m.setPause(r, reason)
			if err := m.o.Clock.Sleep(ctx, pausePoll); err != nil {
				return err
			}
		}
		if paused {
			m.setPause(r, "")
			lim.reset()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return lim.wait(ctx, m.o.Clock, chunkSize)
	}
}

// copyItem 整理一条排队记录（06 文档第 7 节）。
func (m *Manager) copyItem(ctx context.Context, r *shareRT, p params, src config.OrganizeSource, it *Item) error {
	db, now := m.o.DB, m.o.Clock.Now()
	abs := srcAbs(src, it.SrcPath)
	fail := func(err error) error {
		it.State, it.Attempts, it.Error = StateFailed, it.Attempts+1, errText(err)
		return saveItem(db, it, m.o.Clock.Now())
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return fail(errors.New("源文件不见了"))
	}
	if fi.Size() != it.Size || fi.ModTime().UnixNano() != it.Mtime {
		_, err := DeleteItem(db, it.ShareID, it.ID) // 已被改动：回到观察中
		return err
	}
	root, err := os.OpenRoot(p.share.Path)
	if err != nil {
		return fail(errors.New("片库离线"))
	}
	defer root.Close()
	dir, name := path.Split(it.TargetPath)
	dir = strings.TrimSuffix(dir, "/")
	if dir != "" { // 保持结构时可能直接放在库目录根
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fail(err)
		}
	}
	move := src.Mode == config.OrganizeMove

	m.copyMu.Lock()
	defer m.copyMu.Unlock()

	final, dup, err := chooseName(root, dir, name, it.Size, it.Fingerprint)
	if err != nil {
		return fail(err)
	}
	if dup { // 目标同名同内容：记为重复，不复制；移动模式源文件照常处理
		it.TargetPath, it.State, it.DoneAt, it.Error = final, StateDuplicate, now, ""
		it.SourceAction = m.handleSource(src, abs, move)
		return saveItem(db, it, now)
	}
	if move && m.o.SameVolume(abs, p.share.Path) { // 同分区移动：直接重命名
		if err := moveFile(abs, filepath.Join(p.share.Path, filepath.FromSlash(final))); err != nil {
			return fail(err)
		}
		it.TargetPath, it.State, it.SourceAction, it.DoneAt, it.Error = final, StateDone, ActRenamed, m.o.Clock.Now(), ""
		return saveItem(db, it, it.DoneAt)
	}
	// 剩余空间：库盘剩余 − 文件大小 ≥ minFree
	if free, total, err := m.o.FreeSpace(p.share.Path); err == nil {
		need := p.minFreeBytes(total)
		r.mu.Lock()
		r.free, r.need = free, need
		r.mu.Unlock()
		if free < need || free-need < uint64(it.Size) {
			m.setPause(r, "lowSpace")
			return errLowSpace
		}
	}

	f, err := openSourceRead(abs)
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	it.State = StateCopying
	if err := saveItem(db, it, now); err != nil {
		return err
	}
	r.mu.Lock()
	r.state = "copying"
	r.cur = &Current{Name: path.Base(it.SrcPath), Total: it.Size, start: m.o.Clock.Now()}
	r.mu.Unlock()
	lim := &limiter{rate: p.rate}
	tmp, err := copyToTemp(ctx, copyJob{src: f, size: it.Size, fp: it.Fingerprint, mtime: fi.ModTime(), root: root, dir: dir,
		gate: m.gate(r, p, src, lim), progress: func(n int64) { m.progress(r, n) }})
	r.mu.Lock()
	r.state, r.cur = "scanning", nil
	r.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil { // 退出：临时文件已删除，记录回到排队
			it.State = StateQueued
			saveItem(db, it, m.o.Clock.Now())
			return ctx.Err()
		}
		return fail(err)
	}
	// 源文件在复制期间被改动 → 丢弃临时文件，回到观察中
	if cur, err := f.Stat(); err != nil || cur.Size() != it.Size || cur.ModTime().UnixNano() != it.Mtime {
		root.Remove(tmp)
		_, err := DeleteItem(db, it.ShareID, it.ID)
		return err
	}
	// 重新选名（复制期间目标目录可能出现同名文件）后原子重命名
	final, dup, err = chooseName(root, dir, name, it.Size, it.Fingerprint)
	if err != nil {
		root.Remove(tmp)
		return fail(err)
	}
	if dup {
		root.Remove(tmp)
		it.State = StateDuplicate
	} else {
		if err := root.Rename(tmp, final); err != nil {
			root.Remove(tmp)
			return fail(err)
		}
		it.State = StateDone
	}
	f.Close()
	it.TargetPath, it.DoneAt, it.Error = final, m.o.Clock.Now(), ""
	it.SourceAction = m.handleSource(src, abs, move)
	return saveItem(db, it, it.DoneAt)
}

// handleSource 按模式处理源文件：复制保留；移动按 afterMove 移入回收站（失败则保留并记 recycle_failed）或永久删除。
func (m *Manager) handleSource(src config.OrganizeSource, abs string, move bool) string {
	if !move {
		return ActKept
	}
	if src.AfterMove == config.AfterMoveDelete {
		if err := os.Remove(abs); err != nil {
			return ActRecycleFailed
		}
		return ActDeleted
	}
	if err := m.o.Recycler.Recycle(abs); err != nil {
		return ActRecycleFailed
	}
	return ActRecycled
}

func (m *Manager) progress(r *shareRT, n int64) {
	now := m.o.Clock.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil {
		return
	}
	r.cur.Bytes = n
	if el := now.Sub(r.cur.start).Seconds(); el > 0 {
		r.cur.Speed = float64(n) / el
	}
}

// errText 把错误转为不含真实路径的可读文字。
func errText(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		err = le.Err
	}
	return err.Error()
}

// ---------- 状态 ----------

// Status 为片库整理状态（06 文档第 11 节）。
type Status struct {
	State        string             `json:"state"`
	PauseReason  string             `json:"pauseReason,omitempty"`
	Error        string             `json:"error,omitempty"`
	LastScanAt   *time.Time         `json:"lastScanAt"`
	NextScanAt   *time.Time         `json:"nextScanAt"`
	Counts       Counts             `json:"counts"`
	BySource     map[string]*Counts `json:"bySource"`
	Current      *Current           `json:"current"`
	FreeBytes    uint64             `json:"freeBytes"`
	MinFreeBytes uint64             `json:"minFreeBytes"`
}

// Status 返回片库状态。暂停原因优先级：手动 > 空间不足 > 正在播放。
func (m *Manager) Status(id string) (Status, error) {
	cfg := m.o.Config.Get()
	p, ok := resolve(cfg, id)
	r := m.rt(id)
	r.mu.Lock()
	st := Status{State: r.state, PauseReason: r.reason, Error: r.errMsg, Counts: Counts{}, BySource: map[string]*Counts{}}
	if !r.lastScan.IsZero() {
		t := r.lastScan
		st.LastScanAt = &t
	}
	if !r.nextScan.IsZero() && ok && p.org.Enabled {
		t := r.nextScan
		st.NextScanAt = &t
	}
	if r.cur != nil {
		c := *r.cur
		st.Current = &c
	}
	watching := r.watching
	r.mu.Unlock()

	if ok {
		if free, total, err := m.o.FreeSpace(p.share.Path); err == nil {
			st.FreeBytes, st.MinFreeBytes = free, p.minFreeBytes(total)
		} else if st.State == "idle" {
			st.State = "offline"
		}
		if p.org.Paused {
			st.PauseReason = "manual"
		}
	}
	if st.PauseReason != "" && st.State != "offline" {
		st.State = "paused"
	}
	total, by, err := countItems(m.o.DB, id)
	if err != nil {
		return st, err
	}
	st.Counts, st.BySource = total, by
	st.Counts.Watching = watching
	return st, nil
}

// ---------- 试运行 ----------

// DryItem 为试运行的一行（06 文档第 11 节）。
type DryItem struct {
	SourceID   string `json:"sourceId"`
	SrcPath    string `json:"srcPath"`
	Category   string `json:"category"`
	TargetPath string `json:"targetPath"`
	Action     string `json:"action"` // copy move rename duplicate waiting locked done skip
	Note       string `json:"note,omitempty"`
}

const dryRunMax = 500

// DryRun 列出“哪个文件会被整理到哪里”，不复制、不写记录、不改观察状态。
func (m *Manager) DryRun(id string) ([]DryItem, int, error) {
	p, ok := resolve(m.o.Config.Get(), id)
	if !ok {
		return []DryItem{}, 0, nil
	}
	if fi, err := os.Stat(p.share.Path); err != nil || !fi.IsDir() {
		return nil, 0, errors.New("片库离线")
	}
	root, err := os.OpenRoot(p.share.Path)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	items := []DryItem{}
	total := 0
	for _, src := range p.org.Sources {
		entries, err := scanSource(src.Path)
		if err != nil {
			continue
		}
		var planned []Item // 视频的计划目标（供字幕跟随）
		var subs []entry
		for _, e := range entries {
			if e.Kind == KindSubtitle {
				subs = append(subs, e)
				continue
			}
			total++
			d := m.dryOne(root, p, src, e)
			if e.Kind == KindVideo && d.Action != "skip" {
				planned = append(planned, Item{SrcKey: strings.ToLower(e.Rel), TargetPath: d.TargetPath, Category: d.Category})
			}
			if len(items) < dryRunMax {
				items = append(items, d)
			}
		}
		for _, e := range subs {
			target, cat, ok := subtitleTarget(planned, e.Rel)
			if !ok {
				continue // 孤立字幕不处理
			}
			d := DryItem{src.ID, e.Rel, cat, target, actionFor(src, p, m), "字幕跟随视频"}
			if rec, _ := latestBySrc(m.o.DB, p.share.ID, src.ID, strings.ToLower(e.Rel)); rec != nil && rec.Size == e.Size &&
				rec.Mtime == e.Mtime.UnixNano() && (rec.State == StateDone || rec.State == StateDuplicate) {
				d.TargetPath, d.Action, d.Note = rec.TargetPath, "done", "已整理过"
			}
			total++
			if len(items) < dryRunMax {
				items = append(items, d)
			}
		}
	}
	return items, total, nil
}

func actionFor(src config.OrganizeSource, p params, m *Manager) string {
	if src.Mode != config.OrganizeMove {
		return "copy"
	}
	if m.o.SameVolume(src.Path, p.share.Path) {
		return "rename"
	}
	return "move"
}

func (m *Manager) dryOne(root *os.Root, p params, src config.OrganizeSource, e entry) DryItem {
	d := DryItem{SourceID: src.ID, SrcPath: e.Rel}
	if rec, _ := latestBySrc(m.o.DB, p.share.ID, src.ID, strings.ToLower(e.Rel)); rec != nil && rec.Size == e.Size &&
		rec.Mtime == e.Mtime.UnixNano() && (rec.State == StateDone || rec.State == StateDuplicate) {
		d.Category, d.TargetPath, d.Action, d.Note = rec.Category, rec.TargetPath, "done", "已整理过"
		return d
	}
	if _, ok := planFor(src, e, nil); !ok {
		d.Action, d.Note = "skip", "路径里有不能用的名字，不整理"
		return d
	}
	if e.Partial {
		plan, _ := planFor(src, e, nil)
		d.Category, d.TargetPath, d.Action, d.Note = plan.Category, plan.Target(), "waiting", "下载中，暂不整理"
		return d
	}
	f, err := openSourceRead(srcAbs(src, e.Rel))
	if err != nil {
		plan, _ := planFor(src, e, nil)
		d.Category, d.TargetPath, d.Action, d.Note = plan.Category, plan.Target(), "locked", "被其他程序占用（可能在下载或做种）"
		return d
	}
	defer f.Close()
	plan, _ := planFor(src, e, photoTimeFunc(f, e.Rel))
	d.Category, d.TargetPath, d.Action = plan.Category, plan.Target(), actionFor(src, p, m)
	if fi, err := root.Lstat(plan.Target()); err == nil && fi.Mode().IsRegular() && fi.Size() == e.Size {
		if fp, err := fingerprint(f, e.Size); err == nil && verify(root, plan.Target(), e.Size, fp) == nil {
			d.Action, d.Note = "duplicate", "片库里已有相同文件，跳过"
		}
	} else if err == nil {
		d.Note = "目标已有同名文件，将加 (1)"
	}
	return d
}
