package episode

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		want Info
		ok   bool
	}{
		// S01E02
		{"Show.Name.S01E12.1080p.mkv", Info{Show: "Show Name", Season: 1, Episode: 12}, true},
		{"Show.Name.S01E02.1080p.mkv", Info{Show: "Show Name", Season: 1, Episode: 2}, true},
		{"show name s1e2", Info{Show: "show name", Season: 1, Episode: 2}, true},
		{"Show_Name_S02E03_x264.mp4", Info{Show: "Show Name", Season: 2, Episode: 3}, true},
		{"Show.Name.S01E02E03.720p.mkv", Info{Show: "Show Name", Season: 1, Episode: 2}, true},
		{"Show.Name.2024.S03E10.WEB-DL.H.265.mkv", Info{Show: "Show Name", Season: 3, Episode: 10}, true},
		{"[Group] Show Name S01 E05 [1080p].mkv", Info{Show: "Show Name", Season: 1, Episode: 5}, true},
		// EP / E
		{"剧名 EP02.mp4", Info{Show: "剧名", Episode: 2}, true},
		{"剧名EP02.mp4", Info{Show: "剧名", Episode: 2}, true},
		{"E12.mkv", Info{Episode: 12}, true},
		{"E12", Info{Episode: 12}, true},
		{"Show.Name.E05.1080p.x265.mkv", Info{Show: "Show Name", Episode: 5}, true},
		{"Show Episode 7.mp4", Info{Show: "Show", Episode: 7}, true},
		// 第N集
		{"剧名 第02集.mp4", Info{Show: "剧名", Episode: 2}, true},
		{"剧名 第12话.mkv", Info{Show: "剧名", Episode: 12}, true},
		{"第12集.mp4", Info{Episode: 12}, true},
		{"剧名 第二集", Info{Show: "剧名", Episode: 2}, true},
		{"剧名 第十二集.mp4", Info{Show: "剧名", Episode: 12}, true},
		{"剧名 第二十集.mp4", Info{Show: "剧名", Episode: 20}, true},
		{"剧名 第一百零五話.mp4", Info{Show: "剧名", Episode: 105}, true},
		{"剧名 第二季 第03集.mp4", Info{Show: "剧名", Season: 2, Episode: 3}, true},
		{"剧名.2023.第05集.1080p.mp4", Info{Show: "剧名", Episode: 5}, true},
		// [12]
		{"[Sub][12][1080P].mkv", Info{Episode: 12}, true},
		{"[字幕组] Show [12].chs.ass", Info{Show: "Show", Episode: 12}, true},
		{"[字幕组][剧名][07][1080P][GB].mp4", Info{Show: "剧名", Episode: 7}, true},
		{"[12].mkv", Info{Episode: 12}, true},
		{"【字幕组】剧名【03】.mp4", Info{Show: "剧名", Episode: 3}, true},
		{"[Group] Show [2024][08].mkv", Info{Show: "Show", Episode: 8}, true},
		// " - 02 "
		{"[字幕组] 剧名 - 02 [1080p].mkv", Info{Show: "剧名", Episode: 2}, true},
		{"Show - 12 [x265].mkv", Info{Show: "Show", Episode: 12}, true},
		{"[Group] Show Name - 03v2 (1080p).mkv", Info{Show: "Show Name", Episode: 3}, true},
		{"Show S2 - 04.mkv", Info{Show: "Show", Season: 2, Episode: 4}, true},
		// 纯集号
		{"12.mkv", Info{Episode: 12}, true},
		{"03.mp4", Info{Episode: 3}, true},
		// 反例：分辨率、年份、编码、普通名称
		{"Movie.2023.1080p.x265.mkv", Info{}, false},
		{"Movie (2009).mkv", Info{}, false},
		{"2024.mp4", Info{}, false},
		{"1080p.mp4", Info{}, false},
		{"x264.mkv", Info{}, false},
		{"Movie.H.265.10bit.mkv", Info{}, false},
		{"Dune.Part.Two.2024.2160p.WEB-DL.DDP5.1.Atmos.H.265.mkv", Info{}, false},
		{"[Group] Movie [1080P][HEVC].mkv", Info{}, false},
		{"Show - 2024 [1080p].mkv", Info{}, false},
		{"[2024].mkv", Info{}, false},
		{"VID_20240101_123456.mp4", Info{}, false},
		{"Movie.E-AC3.mkv", Info{}, false},
		{"notes.txt", Info{}, false},
		{"Season 2", Info{}, false},
		{"", Info{}, false},
	}
	for _, c := range cases {
		got, ok := Parse(c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestParseWithDir(t *testing.T) {
	cases := []struct {
		dir, name string
		want      Info
		ok        bool
	}{
		{"进击的巨人", "12.mkv", Info{Show: "进击的巨人", Episode: 12}, true},
		{"动漫/某剧", "第12集.mp4", Info{Show: "某剧", Episode: 12}, true},
		{"Some.Show.2019", "[02].mkv", Info{Show: "Some Show", Episode: 2}, true},
		{"进击的巨人/第二季", "05.mkv", Info{Show: "进击的巨人", Season: 2, Episode: 5}, true},
		{`Show\Season 3`, "E01.mkv", Info{Show: "Show", Season: 3, Episode: 1}, true},
		{"Other", "Show.S01E02.mkv", Info{Show: "Show", Season: 1, Episode: 2}, true}, // 文件名已有剧名，不取目录
		{"", "12.mkv", Info{Episode: 12}, true},
		{"进击的巨人", "readme.txt", Info{}, false},
	}
	for _, c := range cases {
		got, ok := ParseWithDir(c.dir, c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseWithDir(%q, %q) = %+v, %v; want %+v, %v", c.dir, c.name, got, ok, c.want, c.ok)
		}
	}
}
