package progress

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"lfb/server/internal/store"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// 看完判定：position ≥ 90% × duration，或片长 ≥ 10 分钟且剩余 < 120 秒（03 文档 4.8）。
func TestWatched(t *testing.T) {
	for _, c := range []struct {
		name     string
		pos, dur float64
		want     bool
	}{
		{"开头", 0, 3000, false},
		{"差一点到 90%（剩余 300.1 秒）", 2699.9, 3000, false},
		{"恰好 90%", 2700, 3000, true},
		{"长片 89.995%（剩余 2001 秒）", 17999, 20000, false},
		{"10 分钟片剩余恰好 120 秒（需 < 120）", 480, 600, false},
		{"10 分钟片剩余 119.9 秒", 480.1, 600, true},
		{"不足 10 分钟只看 90%：剩余 119.9 秒", 479.1, 599, false},
		{"短片 50%（剩余 50 秒）", 50, 100, false},
		{"短片 90%", 90, 100, true},
		{"超过时长", 1100, 1000, true},
		{"时长未知", 0, 0, false},
	} {
		if got := Watched(c.pos, c.dur); got != c.want {
			t.Errorf("%s: Watched(%v, %v) = %v, want %v", c.name, c.pos, c.dur, got, c.want)
		}
	}
}
func TestSaveAndInDir(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	t0 := time.UnixMilli(1_700_000_000_000)
	recs := []Record{
		{Device: "d1", Share: "s1", Path: "番剧/E01.mkv", Position: 100, Duration: 1000, UpdatedAt: t0},
		{Device: "d1", Share: "s1", Path: "番剧/E02.mkv", Position: 950, Duration: 1000, UpdatedAt: t0},
		{Device: "d1", Share: "s1", Path: "番剧/sub/E03.mkv", Position: 1, Duration: 1000, UpdatedAt: t0}, // 子目录，不属于 番剧/
		{Device: "d1", Share: "s1", Path: "根.mkv", Position: 5, Duration: 1000, UpdatedAt: t0},
		{Device: "d2", Share: "s1", Path: "番剧/E01.mkv", Position: 500, Duration: 1000, UpdatedAt: t0}, // 其他设备
		{Device: "d1", Share: "s2", Path: "番剧/E01.mkv", Position: 600, Duration: 1000, UpdatedAt: t0}, // 其他共享
	}
	for _, r := range recs {
		if err := Save(ctx, db, r); err != nil {
			t.Fatal(err)
		}
	}
	// 覆盖写：同设备同文件（路径大小写不同）只保留一条
	if err := Save(ctx, db, Record{Device: "d1", Share: "s1", Path: "番剧/e01.MKV", Position: 200, Duration: 1000, UpdatedAt: t0.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	m, err := InDir(ctx, db, "d1", "s1", "番剧")
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"e01.mkv", "e02.mkv"}) {
		t.Fatalf("InDir 键 = %q", keys)
	}
	if e := m["e01.mkv"]; e.Position != 200 || e.Watched {
		t.Errorf("E01 = %+v", e)
	}
	if e := m["e02.mkv"]; !e.Watched {
		t.Errorf("E02 应已看完: %+v", e)
	}

	root, err := InDir(ctx, db, "d1", "s1", "")
	if err != nil || len(root) != 1 || root["根.mkv"].Position != 5 {
		t.Fatalf("根目录 = %v %v", root, err)
	}
	// 反斜杠与大小写不影响目录匹配
	if m2, _ := InDir(ctx, db, "d1", "s1", `番剧\`); len(m2) != 0 {
		t.Errorf("非规范目录（尾部分隔符）不应命中: %v", m2)
	}
}

func TestRecentOrderAndFilter(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	t0 := time.UnixMilli(1_700_000_000_000)
	for i, r := range []Record{
		{Device: "d1", Share: "s1", Path: "a/E01.mkv"},
		{Device: "d2", Share: "s1", Path: "a/E02.mkv"},
		{Device: "d1", Share: "hidden", Path: "x/E01.mkv"},
		{Device: "d1", Share: "s2", Path: "b/E01.mkv"},
	} {
		r.Position, r.Duration, r.UpdatedAt = 10, 100, t0.Add(time.Duration(i)*time.Minute)
		if err := Save(ctx, db, r); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	err := Recent(ctx, db, []string{"s1", "s2"}, func(r Record) bool {
		got = append(got, r.Share+":"+r.Path+":"+r.Device)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"s2:b/E01.mkv:d1", "s1:a/E02.mkv:d2", "s1:a/E01.mkv:d1"}
	if !slices.Equal(got, want) {
		t.Fatalf("Recent = %q, want %q", got, want)
	}
	// 回调返回 false 时停止
	n := 0
	Recent(ctx, db, []string{"s1", "s2"}, func(Record) bool { n++; return false })
	if n != 1 {
		t.Errorf("应在第一条后停止，实际 %d", n)
	}
	// 没有可见共享
	if err := Recent(ctx, db, nil, func(Record) bool { t.Fatal("不应有记录"); return true }); err != nil {
		t.Fatal(err)
	}
}
