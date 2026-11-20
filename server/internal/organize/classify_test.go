package organize

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	mt := time.Date(2024, 3, 9, 10, 0, 0, 0, time.Local)
	exif := func() (time.Time, bool) { return time.Date(2023, 12, 25, 8, 0, 0, 0, time.Local), true }
	cases := []struct {
		rel      string
		exif     bool
		cat, out string
	}{
		{"IMG_20250101_120000.jpg", true, CatPhoto, "照片/2023/12/IMG_20250101_120000.jpg"},
		{"a/pic.png", false, CatPhoto, "照片/2024/03/pic.png"},
		// 无 EXIF：先取文件名中的合法日期，取不到再用修改时间
		{"IMG_20250314_101010.jpg", false, CatPhoto, "照片/2025/03/IMG_20250314_101010.jpg"},
		{"PXL_20220705_123456789.jpg", false, CatPhoto, "照片/2022/07/PXL_20220705_123456789.jpg"},
		{"Screenshot_2025-03-14-10-10-10.png", false, CatPhoto, "照片/2025/03/Screenshot_2025-03-14-10-10-10.png"},
		{"Screenshot_20250314-101010_微信.png", false, CatPhoto, "照片/2025/03/Screenshot_20250314-101010_微信.png"},
		{"IMG-20230110-WA0001.jpeg", false, CatPhoto, "照片/2023/01/IMG-20230110-WA0001.jpeg"},
		{"mmexport_2021_11_02.jpg", false, CatPhoto, "照片/2021/11/mmexport_2021_11_02.jpg"},
		{"IMG_20251340_101010.jpg", false, CatPhoto, "照片/2024/03/IMG_20251340_101010.jpg"}, // 13 月非法
		{"IMG_20250230.jpg", false, CatPhoto, "照片/2024/03/IMG_20250230.jpg"},               // 2 月 30 日非法
		{"DSC120250314.jpg", false, CatPhoto, "照片/2024/03/DSC120250314.jpg"},               // 数字串中间不算日期
		{"IMG_1234.jpg", false, CatPhoto, "照片/2024/03/IMG_1234.jpg"},
		{"VID_20250102_101010.mp4", false, CatPhoto, "照片/2025/01/VID_20250102_101010.mp4"},
		{"PXL_20240705_000000.mp4", false, CatPhoto, "照片/2024/07/PXL_20240705_000000.mp4"},
		{"IMG_1234.MOV", false, CatPhoto, "照片/2024/03/IMG_1234.MOV"},
		{"VID-20230110-WA0001.mp4", false, CatPhoto, "照片/2023/01/VID-20230110-WA0001.mp4"},
		{"Show.Name.S01E02.1080p.mkv", false, CatEpisode, "剧集/Show Name/第1季/Show.Name.S01E02.1080p.mkv"},
		{"x/Show.Name.S02E10.WEB-DL.mkv", false, CatEpisode, "剧集/Show Name/第2季/Show.Name.S02E10.WEB-DL.mkv"},
		{"[字幕组] 剧名 - 02 [1080p].mkv", false, CatEpisode, "剧集/剧名/第1季/[字幕组] 剧名 - 02 [1080p].mkv"},
		{"剧名 第02集.mp4", false, CatEpisode, "剧集/剧名/第1季/剧名 第02集.mp4"},
		{"剧名 EP02.mp4", false, CatEpisode, "剧集/剧名/第1季/剧名 EP02.mp4"},
		{"进击的巨人/12.mkv", false, CatEpisode, "剧集/进击的巨人/第1季/12.mkv"},
		{"某剧/第二季/第12集.mp4", false, CatEpisode, "剧集/某剧/第2季/第12集.mp4"},
		{"12.mp4", false, CatUnsorted, "未分类/12.mp4"},
		{"[02].mkv", false, CatUnsorted, "未分类/[02].mkv"},
		{"Inception.2010.1080p.BluRay.x264.mkv", false, CatMovie, "电影/Inception (2010)/Inception.2010.1080p.BluRay.x264.mkv"},
		{"some movie.mp4", false, CatMovie, "电影/some movie.mp4"},
		{"1080p.mkv", false, CatUnsorted, "未分类/1080p.mkv"},
		{"Bad:Name?S01E01.mkv", false, CatEpisode, "剧集/Bad Name/第1季/Bad:Name?S01E01.mkv"},
		{"CON.S01E01.mkv", false, CatEpisode, "剧集/CON_/第1季/CON.S01E01.mkv"},
	}
	for _, c := range cases {
		var pt func() (time.Time, bool)
		if c.exif {
			pt = exif
		}
		p := classify(c.rel, mt, pt)
		if p.Category != c.cat || p.Target() != c.out {
			t.Errorf("%s: %s %s, want %s %s", c.rel, p.Category, p.Target(), c.cat, c.out)
		}
	}
}

func TestSafeDirName(t *testing.T) {
	for in, want := range map[string]string{
		"a<b>c":  "a b c",
		"name. ": "name",
		"aux":    "aux_",
		"Nul.x":  "Nul_.x",
		"  ":     "",
		"ok":     "ok",
	} {
		if got := safeDirName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestTempAndKind(t *testing.T) {
	for _, n := range []string{"a.mkv.part", "a.!qB", "a.td", "x.downloading", "y.crdownload", "z.mkv.aria2", "t.TMP"} {
		if !isTempName(n) {
			t.Errorf("%s 应为临时文件", n)
		}
	}
	if isTempName("a.mkv") || kindOf("a.MTS") != KindVideo || kindOf("a.heic") != KindImage || kindOf("a.ass") != KindSubtitle || kindOf("a.nfo") != "" {
		t.Error("种类判定错误")
	}
}

// jpegWithExif 构造只含 SOI + APP1(Exif) + EOI 的最小 JPEG。
func jpegWithExif(dt string, le bool) []byte {
	var bo binary.ByteOrder = binary.BigEndian
	hdr := "MM"
	if le {
		bo, hdr = binary.LittleEndian, "II"
	}
	tiff := make([]byte, 8+2+12+4+2+12+4+20)
	copy(tiff, hdr)
	bo.PutUint16(tiff[2:], 42)
	bo.PutUint32(tiff[4:], 8)
	bo.PutUint16(tiff[8:], 1) // IFD0：1 项 ExifIFD 指针
	bo.PutUint16(tiff[10:], 0x8769)
	bo.PutUint16(tiff[12:], 4)
	bo.PutUint32(tiff[14:], 1)
	bo.PutUint32(tiff[18:], 26)
	bo.PutUint16(tiff[26:], 1) // Exif IFD：DateTimeOriginal
	bo.PutUint16(tiff[28:], 0x9003)
	bo.PutUint16(tiff[30:], 2)
	bo.PutUint32(tiff[32:], 20)
	bo.PutUint32(tiff[36:], 44)
	copy(tiff[44:], dt+"\x00")
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 4, 0, 0}) // 一个空 APP0 段
	b.Write([]byte{0xFF, 0xE1})
	binary.Write(&b, binary.BigEndian, uint16(len(app1)+2))
	b.Write(app1)
	b.Write([]byte{0xFF, 0xD9})
	return b.Bytes()
}

func TestExifDateTime(t *testing.T) {
	for _, le := range []bool{true, false} {
		got, ok := exifDateTime(bytes.NewReader(jpegWithExif("2022:05:06 07:08:09", le)))
		if !ok || !got.Equal(time.Date(2022, 5, 6, 7, 8, 9, 0, time.Local)) {
			t.Errorf("le=%v: %v %v", le, got, ok)
		}
	}
	for _, bad := range [][]byte{nil, []byte("not a jpeg"), {0xFF, 0xD8, 0xFF, 0xDA, 0, 2}, jpegWithExif("garbage-garbage-xxx", true)} {
		if _, ok := exifDateTime(bytes.NewReader(bad)); ok {
			t.Errorf("应解析失败: %q", bad)
		}
	}
}
