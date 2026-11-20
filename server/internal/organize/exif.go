package organize

import (
	"bytes"
	"encoding/binary"
	"io"
	"time"
)

// exifDateTime 从 JPEG 的 APP1 Exif 段读取 DateTimeOriginal（0x9003，位于 Exif 子 IFD）。
// 只读文件开头的标记段，不解码图像；没有或格式不对时 ok 为 false。时间按本地时区解释。
func exifDateTime(r io.Reader) (t time.Time, ok bool) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil || head[0] != 0xFF || head[1] != 0xD8 {
		return t, false
	}
	for range 32 { // 最多看 32 个标记段
		var m [4]byte
		if _, err := io.ReadFull(r, m[:]); err != nil || m[0] != 0xFF {
			return t, false
		}
		marker, size := m[1], int(binary.BigEndian.Uint16(m[2:]))
		if marker == 0xDA || marker == 0xD9 || size < 2 { // 图像数据开始 / 结束
			return t, false
		}
		seg := make([]byte, size-2)
		if _, err := io.ReadFull(r, seg); err != nil {
			return t, false
		}
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffDateTimeOriginal(seg[6:])
		}
	}
	return t, false
}

func tiffDateTimeOriginal(b []byte) (time.Time, bool) {
	if len(b) < 8 {
		return time.Time{}, false
	}
	var bo binary.ByteOrder
	switch string(b[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return time.Time{}, false
	}
	// entry 在 IFD 中查找 tag，返回 (type, count, value/offset 字段)
	entry := func(ifd uint32, tag uint16) (uint16, uint32, uint32, bool) {
		if int(ifd)+2 > len(b) {
			return 0, 0, 0, false
		}
		n := int(bo.Uint16(b[ifd:]))
		for i := 0; i < n; i++ {
			o := int(ifd) + 2 + 12*i
			if o+12 > len(b) {
				return 0, 0, 0, false
			}
			if bo.Uint16(b[o:]) == tag {
				return bo.Uint16(b[o+2:]), bo.Uint32(b[o+4:]), bo.Uint32(b[o+8:]), true
			}
		}
		return 0, 0, 0, false
	}
	_, _, exifIFD, ok := entry(bo.Uint32(b[4:]), 0x8769)
	if !ok {
		return time.Time{}, false
	}
	typ, count, off, ok := entry(exifIFD, 0x9003)
	if !ok || typ != 2 || count < 19 || int(off)+19 > len(b) {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006:01:02 15:04:05", string(b[off:off+19]), time.Local)
	return t, err == nil && t.Year() > 1900
}
