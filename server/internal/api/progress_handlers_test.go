package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfb/server/internal/activity"
	"lfb/server/internal/auth"
	"lfb/server/internal/config"
	"lfb/server/internal/store"
)

// newProgressServer 在 newFileServer 基础上接入 data.db 与活跃播放跟踪。
func newProgressServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, root := newFileServer(t)
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s.Store = db
	s.Activity = activity.New(activity.Options{})
	return s, root
}

func postProgress(t *testing.T, s *Server, tok, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/progress", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func progBody(share, path string, pos, dur float64) string {
	b, _ := json.Marshal(map[string]any{"share": share, "path": path, "position": pos, "duration": dur})
	return string(b)
}

func mustPost(t *testing.T, s *Server, tok, share, path string, pos, dur float64) {
	t.Helper()
	if w := postProgress(t, s, tok, progBody(share, path, pos, dur)); w.Code != 204 {
		t.Fatalf("上报 %s/%s: %d %s", share, path, w.Code, w.Body)
	}
}

func listProgress(t *testing.T, s *Server, tok, share, dir string) map[string]map[string]any {
	t.Helper()
	lr := list(t, s, tok, q("/api/list", share, dir))
	m := map[string]map[string]any{}
	for _, it := range lr.Items {
		if it.Progress != nil {
			m[it.Name] = it.Progress.(map[string]any)
		}
	}
	return m
}

func TestProgressReportAndList(t *testing.T) {
	s, root := newProgressServer(t)
	mustPost(t, s, "tok1", "pub", "video.mkv", 100, 1000)
	mustPost(t, s, "tok1", "pub", "Z目录/x.mp4", 50, 100) // 子目录，不出现在根列表
	mustPost(t, s, "tok2", "pub", "第1集.mkv", 2700, 3000)

	m := listProgress(t, s, "tok1", "pub", "")
	if len(m) != 1 || m["video.mkv"]["position"] != 100.0 || m["video.mkv"]["duration"] != 1000.0 || m["video.mkv"]["watched"] != false {
		t.Fatalf("dev1 根目录进度 = %v", m)
	}
	// 列表只含本设备进度：dev2 只看到自己的 第1集（恰好 90% → 已看完）
	m = listProgress(t, s, "tok2", "pub", "")
	if len(m) != 1 || m["第1集.mkv"]["watched"] != true {
		t.Fatalf("dev2 根目录进度 = %v", m)
	}
	if m := listProgress(t, s, "tok1", "pub", "Z目录"); m["x.mp4"]["position"] != 50.0 {
		t.Fatalf("子目录进度 = %v", m)
	}
	// 进度按共享区分：priv 与 pub 指向同一目录，但记录不共用
	if m := listProgress(t, s, "tok1", "priv", ""); len(m) != 0 {
		t.Fatalf("priv 不应有进度: %v", m)
	}
	// 上报计入活跃播放（按卷）
	if !s.Activity.ActiveOn(activity.VolumeOf(root)) {
		t.Error("上报进度后应处于活跃播放")
	}
}

// 进度变化后列表 ETag 随之变化，避免 304 返回旧进度。
func TestProgressChangesListETag(t *testing.T) {
	s, _ := newProgressServer(t)
	target := q("/api/list", "pub", "")
	e1 := fget(t, s, "tok1", target).Header().Get("ETag")
	mustPost(t, s, "tok1", "pub", "video.mkv", 100, 1000)
	e2 := fget(t, s, "tok1", target).Header().Get("ETag")
	mustPost(t, s, "tok1", "pub", "video.mkv", 200, 1000)
	e3 := fget(t, s, "tok1", target).Header().Get("ETag")
	if e1 == e2 || e2 == e3 {
		t.Fatalf("ETag 未随进度变化: %s %s %s", e1, e2, e3)
	}
	// 304 路径由 TestListETag 覆盖；此处直接比较 listETag，避免新建文件后 NTFS 延迟更新目录项 mtime 造成抖动
	items := []listItem{{Name: "a.mkv", Kind: "video", Progress: &itemProgress{100, 1000, false}}}
	same := []listItem{{Name: "a.mkv", Kind: "video", Progress: &itemProgress{100, 1000, false}}}
	moved := []listItem{{Name: "a.mkv", Kind: "video", Progress: &itemProgress{101, 1000, false}}}
	etag := func(it []listItem) string { return listETag(it, config.Perms{}, "name", "asc", 0, 200) }
	if etag(items) != etag(same) || etag(items) == etag(moved) || etag(items) == etag([]listItem{{Name: "a.mkv", Kind: "video"}}) {
		t.Error("listETag 应且仅应随进度变化")
	}
}

func TestProgressWatchedBoundaryAndClamp(t *testing.T) {
	s, _ := newProgressServer(t)
	for _, c := range []struct {
		pos, dur float64
		want     bool
	}{
		{2699, 3000, false}, // 89.97%，剩余 301 秒
		{2700, 3000, true},  // 90%
		{480, 600, false},   // 剩余恰好 120 秒
		{481, 600, true},    // 剩余 119 秒
		{81, 200, false},    // 不足 10 分钟：剩余 119 秒仍未看完
		{1200, 1000, true},  // 超出时长 → 截到时长
	} {
		mustPost(t, s, "tok1", "pub", "video.mkv", c.pos, c.dur)
		p := listProgress(t, s, "tok1", "pub", "")["video.mkv"]
		if p["watched"] != c.want {
			t.Errorf("%v/%v watched = %v, want %v", c.pos, c.dur, p["watched"], c.want)
		}
		if p["position"].(float64) > c.dur {
			t.Errorf("position %v 超过时长", p["position"])
		}
	}
}

func TestProgressRejects(t *testing.T) {
	s, _ := newProgressServer(t)
	for _, c := range []struct {
		tok, body string
		status    int
		code      string
	}{
		{"tok2", progBody("priv", "video.mkv", 1, 100), 404, CodeNotFound},           // 不可见共享
		{"tok1", progBody("nope", "video.mkv", 1, 100), 404, CodeNotFound},           // 不存在的共享
		{"tok1", progBody("gone", "video.mkv", 1, 100), 503, CodeShareOffline},       // 离线
		{"tok1", progBody("pub", "missing.mkv", 1, 100), 404, CodeNotFound},          // 文件不存在
		{"tok1", progBody("pub", "Z目录", 1, 100), 404, CodeNotFound},                  // 目录
		{"tok1", progBody("pub", "a.TXT", 1, 100), 404, CodeNotFound},                // 非视频
		{"tok1", progBody("pub", ".dot", 1, 100), 404, CodeNotFound},                 // 隐藏项
		{"tok1", progBody("pub", "../video.mkv", 1, 100), 400, CodeBadPath},          // 穿越
		{"tok1", progBody("pub", `..\..\Windows\win.ini`, 1, 100), 400, CodeBadPath}, // 反斜杠穿越
		{"tok1", progBody("pub", "C:/Windows/x.mkv", 1, 100), 400, CodeBadPath},      // 盘符
		{"tok1", progBody("pub", "video.mkv", -1, 100), 400, CodeBadPath},            // 负位置
		{"tok1", progBody("pub", "video.mkv", 1, 0), 400, CodeBadPath},               // 时长 0
		{"tok1", progBody("pub", "video.mkv", 1, 1e12), 400, CodeBadPath},            // 时长异常
		{"tok1", `{"share":"pub","path":"video.mkv"}`, 400, CodeBadPath},             // 缺字段
		{"tok1", `not json`, 400, CodeBadPath},
		{"", progBody("pub", "video.mkv", 1, 100), 401, CodeUnauthorized},
	} {
		wantErr(t, postProgress(t, s, c.tok, c.body), c.status, c.code, c.body)
	}
	if m := listProgress(t, s, "tok1", "pub", ""); len(m) != 0 {
		t.Fatalf("被拒绝的上报不应写入: %v", m)
	}
}

type continueItem struct {
	Share, Path, Name, UpdatedAt, DeviceName string
	Position, Duration                       float64
	Watched                                  bool
	Next                                     *struct{ Name, Path, Kind string }
}

func getContinue(t *testing.T, s *Server, tok string) []continueItem {
	t.Helper()
	w := fget(t, s, tok, "/api/continue")
	var body struct{ Items []continueItem }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("continue: %d %s", w.Code, w.Body)
	}
	return body.Items
}

func TestContinue(t *testing.T) {
	s, root := newProgressServer(t)
	svc, err := auth.New(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	tok1, _ := svc.Store.Add("dev1", "客厅电视")
	tok2, _ := svc.Store.Add("dev2", "手机")
	s.Auth = svc

	writeFiles(t, root, "Z目录/y.mp4")                   // 让看完的 x.mp4 有下一集
	mustPost(t, s, tok1, "priv", "video.mkv", 10, 100) // 仅 dev1 可见的共享
	mustPost(t, s, tok1, "pub", "第1集.mkv", 30, 100)
	time.Sleep(5 * time.Millisecond)
	mustPost(t, s, tok2, "pub", "第2集.mkv", 40, 100) // 同共享同目录，dev2 更晚
	time.Sleep(5 * time.Millisecond)
	mustPost(t, s, tok1, "pub", "Z目录/x.mp4", 95, 100)

	// dev2 看不到 priv；pub 根目录取所有设备中最近一条（dev2 自己的 第2集）
	items := getContinue(t, s, tok2)
	if len(items) != 2 || items[0].Path != "Z目录/x.mp4" || items[1].Path != "第2集.mkv" {
		t.Fatalf("dev2 continue = %+v", items)
	}
	if it := items[0]; it.Name != "x.mp4" || it.DeviceName != "客厅电视" || !it.Watched || it.Position != 95 || it.UpdatedAt == "" {
		t.Errorf("条目字段 = %+v", it)
	}
	if n := items[0].Next; n == nil || n.Path != "Z目录/y.mp4" || n.Name != "y.mp4" || n.Kind != "video" {
		t.Errorf("看完的条目应附下一集: %+v", n)
	}
	if items[1].Next != nil {
		t.Errorf("未看完的条目不应有 next: %+v", items[1].Next)
	}
	for _, it := range items {
		if it.Share == "priv" {
			t.Fatal("不应泄露不可见共享")
		}
	}
	// dev1：家庭共享进度——pub 根目录显示 dev2 的 第2集；另有 priv
	items = getContinue(t, s, tok1)
	got := []string{}
	for _, it := range items {
		got = append(got, it.Share+":"+it.Path+":"+it.DeviceName)
	}
	want := "pub:Z目录/x.mp4:客厅电视 pub:第2集.mkv:手机 priv:video.mkv:客厅电视"
	if strings.Join(got, " ") != want {
		t.Fatalf("dev1 continue = %q, want %q", got, want)
	}

	// 文件被删除：跳过并由同目录较早的记录补位
	if err := os.Remove(filepath.Join(root, "第2集.mkv")); err != nil {
		t.Fatal(err)
	}
	items = getContinue(t, s, tok2)
	if len(items) != 2 || items[1].Path != "第1集.mkv" {
		t.Fatalf("删除后 continue = %+v", items)
	}
}

func TestContinueLimit(t *testing.T) {
	s, root := newProgressServer(t)
	for i := range 25 {
		rel := fmt.Sprintf("剧%02d/E01.mkv", i)
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("v"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustPost(t, s, "tok2", "pub", rel, 1, 100)
	}
	if items := getContinue(t, s, "tok2"); len(items) != maxContinue {
		t.Fatalf("应最多 %d 条，实际 %d", maxContinue, len(items))
	}
	// 未接入存储时返回空列表而非错误
	s.Store = nil
	if items := getContinue(t, s, "tok2"); len(items) != 0 {
		t.Fatalf("无存储: %+v", items)
	}
}

// 看完且无下一集的条目不出现，同目录较早的记录也不补位。
func TestContinueWatchedWithoutNext(t *testing.T) {
	s, _ := newProgressServer(t)
	writeFiles(t, s.Config.Get().Shares[0].Path, "Z目录/a.mp4")
	mustPost(t, s, "tok2", "pub", "Z目录/a.mp4", 50, 100)
	time.Sleep(5 * time.Millisecond)
	mustPost(t, s, "tok2", "pub", "Z目录/x.mp4", 99, 100) // x.mp4 是自然排序最后一个
	mustPost(t, s, "tok2", "pub", "第1集.mkv", 95, 100)   // 看完，有 第2集
	items := getContinue(t, s, "tok2")
	if len(items) != 1 || items[0].Path != "第1集.mkv" || items[0].Next == nil || items[0].Next.Path != "第2集.mkv" {
		t.Fatalf("continue = %+v", items)
	}
}
