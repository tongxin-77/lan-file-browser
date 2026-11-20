package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lfb/server/internal/activity"
)

// activityProbe 在写响应体时记录当时文件所在卷是否处于活跃播放。
type activityProbe struct {
	*httptest.ResponseRecorder
	tr      *activity.Tracker
	vol     string
	wrote   bool
	sawLive bool
}

func (p *activityProbe) Write(b []byte) (int, error) {
	p.wrote = true
	p.sawLive = p.sawLive || p.tr.ActiveOn(p.vol)
	return p.ResponseRecorder.Write(b)
}

// /api/file 的视频响应期间计入活跃播放（按卷），结束后撤销；非视频、/api/download 不计入。
func TestFileStreamActivity(t *testing.T) {
	s, root := newFileServer(t)
	s.Activity = activity.New(activity.Options{})
	vol := activity.VolumeOf(root)
	for _, c := range []struct {
		target string
		live   bool
	}{
		{q("/api/file", "pub", "video.mkv"), true},
		{q("/api/file", "pub", "a.TXT"), false},
		{q("/api/download", "priv", "video.mkv"), false},
	} {
		r := httptest.NewRequest("GET", c.target, nil)
		r.Header.Set("Authorization", "Bearer tok1")
		p := &activityProbe{ResponseRecorder: httptest.NewRecorder(), tr: s.Activity, vol: vol}
		s.Handler().ServeHTTP(p, r)
		if p.Code != http.StatusOK || !p.wrote {
			t.Fatalf("%s: %d", c.target, p.Code)
		}
		if p.sawLive != c.live {
			t.Errorf("%s: 响应期间活跃=%v, want %v", c.target, p.sawLive, c.live)
		}
		if s.Activity.Active() {
			t.Errorf("%s: 响应结束后不应仍活跃", c.target)
		}
	}
}
