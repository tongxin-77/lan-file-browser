package thumb

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"
)

// jpegExif 读取 JPEG 开头 APP1 Exif 段中的 Orientation（IFD0 0x0112，缺省 1）与 IFD1 内嵌缩略图
// （0x0201/0x0202 指向的 JPEG 数据，没有则为 nil）。只读标记段，不解码图像；读完后 r 位置不确定。
func jpegExif(r io.Reader) (orient int, thumb []byte) {
	orient = 1
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil || head[0] != 0xFF || head[1] != 0xD8 {
		return
	}
	for range 32 {
		var m [4]byte
		if _, err := io.ReadFull(r, m[:]); err != nil || m[0] != 0xFF {
			return
		}
		marker, size := m[1], int(binary.BigEndian.Uint16(m[2:]))
		if marker == 0xDA || marker == 0xD9 || size < 2 {
			return
		}
		seg := make([]byte, size-2)
		if _, err := io.ReadFull(r, seg); err != nil {
			return
		}
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffExif(seg[6:])
		}
	}
	return
}

func tiffExif(b []byte) (orient int, thumb []byte) {
	orient = 1
	if len(b) < 8 {
		return
	}
	var bo binary.ByteOrder
	switch string(b[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return
	}
	// ifd 返回 IFD 中各 tag 的 (type, value 字段) 及下一个 IFD 偏移
	ifd := func(off uint32) (map[uint16][2]uint32, uint32) {
		if int(off)+2 > len(b) {
			return nil, 0
		}
		n := int(bo.Uint16(b[off:]))
		end := int(off) + 2 + 12*n
		if end+4 > len(b) {
			return nil, 0
		}
		tags := make(map[uint16][2]uint32, n)
		for i := range n {
			e := b[int(off)+2+12*i:]
			typ := bo.Uint16(e[2:])
			v := bo.Uint32(e[8:])
			if typ == 3 { // SHORT 值在字段前 2 字节
				v = uint32(bo.Uint16(e[8:]))
			}
			tags[bo.Uint16(e)] = [2]uint32{uint32(typ), v}
		}
		return tags, bo.Uint32(b[end:])
	}
	ifd0, next := ifd(bo.Uint32(b[4:]))
	if o, ok := ifd0[0x0112]; ok && o[1] >= 1 && o[1] <= 8 {
		orient = int(o[1])
	}
	if next == 0 {
		return
	}
	ifd1, _ := ifd(next)
	off, ok1 := ifd1[0x0201]
	n, ok2 := ifd1[0x0202]
	if ok1 && ok2 && n[1] > 0 && uint64(off[1])+uint64(n[1]) <= uint64(len(b)) {
		thumb = b[off[1] : off[1]+n[1]]
	}
	return
}

// transposed 报告该方向是否需要交换宽高（5–8）。
func transposed(orient int) bool { return orient >= 5 && orient <= 8 }

// applyOrient 按 EXIF Orientation 把原始像素变换为正向显示。
func applyOrient(src *image.RGBA, orient int) *image.RGBA {
	if orient <= 1 || orient > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if transposed(orient) {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch orient {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			si, di := src.PixOffset(x+src.Rect.Min.X, y+src.Rect.Min.Y), dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
