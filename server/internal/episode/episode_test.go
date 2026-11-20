package episode

import "testing"

// TODO: 实现 Parse 后去掉 t.Skip，并补充用例（03 文档 4.6、06 文档 5.2）。
func TestParse(t *testing.T) {
	t.Skip("TODO: episode.Parse 尚未实现")
	cases := []struct {
		name string
		want Info
		ok   bool
	}{
		{"Show.Name.S01E12.1080p.mkv", Info{Show: "Show Name", Season: 1, Episode: 12}, true},
		{"剧名 EP02.mp4", Info{Show: "剧名", Episode: 2}, true},
		{"剧名 第02集.mp4", Info{Show: "剧名", Episode: 2}, true},
		{"[Sub][12][1080P].mkv", Info{Episode: 12}, true},
		{"[字幕组] 剧名 - 02 [1080p].mkv", Info{Show: "剧名", Episode: 2}, true},
		{"12.mkv", Info{Episode: 12}, true},
		{"Movie.2023.1080p.x265.mkv", Info{}, false},
	}
	for _, c := range cases {
		got, ok := Parse(c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
