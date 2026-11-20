package media

import "testing"

func TestKindAndMIME(t *testing.T) {
	cases := []struct{ name, kind, mime string }{
		{"E12.MKV", KindVideo, "video/x-matroska"},
		{"a.m4v", KindVideo, "video/mp4"},
		{"a.m2ts", KindVideo, "video/mp2t"},
		{"a.mov", KindVideo, "application/octet-stream"},
		{"p.JPEG", KindImage, "image/jpeg"},
		{"s.flac", KindAudio, "audio/flac"},
		{"E12.chs.ass", KindSubtitle, "text/x-ssa"},
		{"s.srt", KindSubtitle, "application/x-subrip"},
		{"readme", KindOther, "application/octet-stream"},
		{"x.exe", KindOther, "application/octet-stream"},
	}
	for _, c := range cases {
		if k, m := Kind(c.name), MIME(c.name); k != c.kind || m != c.mime {
			t.Errorf("%s: %s %s, want %s %s", c.name, k, m, c.kind, c.mime)
		}
	}
}
