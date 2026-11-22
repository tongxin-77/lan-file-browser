package thumb

// Windows 原生出图：Shell 系统缩略图（IShellItemImageFactory）与 WIC JPEG 按比例解码
// （IWICBitmapSourceTransform，DCT 域缩放）。COM 调用都在锁定的 OS 线程上完成。

import (
	"errors"
	"fmt"
	"image"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32  = windows.NewLazySystemDLL("ole32.dll")
	shell  = windows.NewLazySystemDLL("shell32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")
	user32 = windows.NewLazySystemDLL("user32.dll")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSHCreateItem     = shell.NewProc("SHCreateItemFromParsingName")
	procGetObjectW       = gdi32.NewProc("GetObjectW")
	procGetDIBits        = gdi32.NewProc("GetDIBits")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
)

var (
	iidShellItemImageFactory = mustGUID("{bcc18b79-ba16-442f-80c4-8a59c30c463b}")
	clsidWICImagingFactory   = mustGUID("{cacaf262-9370-4615-a13b-9f5539da4c0a}")
	iidWICImagingFactory     = mustGUID("{ec5ec8a9-c395-4314-9c77-54d7a935ff70}")
	iidWICSourceTransform    = mustGUID("{3b16811b-6a43-4ec9-b713-3d5a0c13b940}")
	guidContainerHeif        = mustGUID("{e1e62521-6787-405b-a339-500715b5763f}")
	guidPixel24bppBGR        = mustGUID("{6fddc324-4e03-4bfe-b185-3d77768dc90c}")
	guidPixel8bppGray        = mustGUID("{6fddc324-4e03-4bfe-b185-3d77768dc908}")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// comObj 是 COM 接口指针：首字段指向虚函数表。
type comObj struct{ vtbl *[32]uintptr }

func (o *comObj) call(i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[i], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObj) release() {
	if o != nil {
		o.call(2)
	}
}

func hrErr(what string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s: HRESULT 0x%08X", what, uint32(hr))
	}
	return nil
}

// withCOM 在锁定的 OS 线程上初始化 STA 后执行 f。
func withCOM(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, windows.COINIT_APARTMENTTHREADED)
	if int32(hr) >= 0 { // S_OK / S_FALSE 都需配对 CoUninitialize
		defer procCoUninitialize.Call()
	}
	return f()
}

const (
	siigbfThumbnailOnly = 0x08 // 只要缩略图，不要图标
	siigbfInCacheOnly   = 0x10 // 只从系统缩略图缓存取
	shellTimeout        = 10 * time.Second
	maxStuckShell       = 4
)

var stuckShell atomic.Int32 // 超时仍未返回的 Shell 调用数；过多时停用 Shell，避免线程堆积

// ShellThumbnail 用 IShellItemImageFactory::GetImage 取系统缩略图（已按 EXIF 转正），最长边不超过 box、
// 不放大。先只查系统缩略图缓存；generate 为 true 时未命中再让系统生成。
// path 必须是调用方经 fsx 校验得到的最终真实路径。
func ShellThumbnail(path string, box int, generate bool) (image.Image, error) {
	if stuckShell.Load() >= maxStuckShell {
		return nil, errors.New("shell: 停用（调用卡住）")
	}
	type result struct {
		img image.Image
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var r result
		r.err = withCOM(func() error {
			var err error
			if r.img, err = shellImage(path, box, siigbfThumbnailOnly|siigbfInCacheOnly); err == nil || !generate {
				return err
			}
			r.img, err = shellImage(path, box, siigbfThumbnailOnly)
			return err
		})
		ch <- r
	}()
	select {
	case r := <-ch:
		return r.img, r.err
	case <-time.After(shellTimeout):
		stuckShell.Add(1)
		go func() { <-ch; stuckShell.Add(-1) }()
		return nil, errors.New("shell: 超时")
	}
}

func shellImage(path string, box int, flags uintptr) (image.Image, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var fac *comObj
	hr, _, _ := procSHCreateItem.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&iidShellItemImageFactory)), uintptr(unsafe.Pointer(&fac)))
	if err := hrErr("SHCreateItemFromParsingName", hr); err != nil {
		return nil, err
	}
	defer fac.release()
	size := uintptr(uint32(box)) | uintptr(uint32(box))<<32 // SIZE 按值传递（x64 下打包进一个寄存器）
	var hbm windows.Handle
	if err := hrErr("GetImage", fac.call(3, size, flags, uintptr(unsafe.Pointer(&hbm)))); err != nil {
		return nil, err
	}
	defer procDeleteObject.Call(uintptr(hbm))
	return hbitmapImage(hbm)
}

// hbitmapImage 把 32 位 HBITMAP 读成 RGBA：透明处铺白底（预乘 alpha）；alpha 全 0 视为不透明。
func hbitmapImage(hbm windows.Handle) (image.Image, error) {
	var bm struct {
		typ, width, height, widthBytes int32
		planes, bitsPixel              uint16
		bits                           uintptr
	}
	if r, _, _ := procGetObjectW.Call(uintptr(hbm), unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 || bm.width <= 0 || bm.height <= 0 {
		return nil, errors.New("shell: GetObject 失败")
	}
	w, h := int(bm.width), int(bm.height)
	bi := struct {
		size                 uint32
		width, height        int32
		planes, bitCount     uint16
		compression, sizeImg uint32
		xppm, yppm           int32
		clrUsed, clrImp      uint32
	}{size: 40, width: int32(w), height: -int32(h), planes: 1, bitCount: 32}
	buf := make([]byte, w*h*4)
	dc, _, _ := procGetDC.Call(0)
	n, _, _ := procGetDIBits.Call(dc, uintptr(hbm), 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0)
	procReleaseDC.Call(0, dc)
	if int(n) != h {
		return nil, errors.New("shell: GetDIBits 失败")
	}
	opaque := true
	for i := 3; i < len(buf); i += 4 {
		if buf[i] != 0 {
			opaque = false
			break
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(buf); i += 4 {
		b, g, r, a := buf[i], buf[i+1], buf[i+2], buf[i+3]
		if !opaque {
			b, g, r = over(b, a), over(g, a), over(r, a)
		}
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, 255
	}
	return img, nil
}

// wicFactory 创建 WIC 工厂（调用方须已在 withCOM 内）。
func wicFactory() (*comObj, error) {
	var fac *comObj
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidWICImagingFactory)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidWICImagingFactory)), uintptr(unsafe.Pointer(&fac)))
	return fac, hrErr("CoCreateInstance(WIC)", hr)
}

// DecodeJPEGScaled 用 WIC 按 1/2、1/4、1/8 的 DCT 缩放解码 JPEG，结果宽高不小于 needW×needH
// （原始方向，未按 EXIF 旋转）。只读调用方已打开的句柄，不经路径。
func DecodeJPEGScaled(f *os.File, needW, needH int) (image.Image, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	var img image.Image
	err := withCOM(func() error {
		fac, err := wicFactory()
		if err != nil {
			return err
		}
		defer fac.release()
		var dec, frame, tr *comObj
		// IWICImagingFactory::CreateDecoderFromFileHandle(hFile, vendor, WICDecodeMetadataCacheOnDemand, ppDecoder)
		if err := hrErr("CreateDecoderFromFileHandle", fac.call(5, f.Fd(), 0, 0, uintptr(unsafe.Pointer(&dec)))); err != nil {
			return err
		}
		defer dec.release()
		if err := hrErr("GetFrame", dec.call(13, 0, uintptr(unsafe.Pointer(&frame)))); err != nil { // IWICBitmapDecoder::GetFrame
			return err
		}
		defer frame.release()
		if err := hrErr("QueryInterface(SourceTransform)", frame.call(0, uintptr(unsafe.Pointer(&iidWICSourceTransform)), uintptr(unsafe.Pointer(&tr)))); err != nil {
			return err
		}
		defer tr.release()
		var sw, sh uint32
		if err := hrErr("GetSize", frame.call(3, uintptr(unsafe.Pointer(&sw)), uintptr(unsafe.Pointer(&sh)))); err != nil {
			return err
		}
		dw, dh := sw, sh
		for _, d := range []uint32{8, 4, 2} {
			if cw, ch := (sw+d-1)/d, (sh+d-1)/d; int(cw) >= needW && int(ch) >= needH {
				dw, dh = cw, ch
				break
			}
		}
		if err := hrErr("GetClosestSize", tr.call(4, uintptr(unsafe.Pointer(&dw)), uintptr(unsafe.Pointer(&dh)))); err != nil {
			return err
		}
		pf := guidPixel24bppBGR
		if err := hrErr("GetClosestPixelFormat", tr.call(5, uintptr(unsafe.Pointer(&pf)))); err != nil {
			return err
		}
		bpp := 0
		switch pf {
		case guidPixel24bppBGR:
			bpp = 3
		case guidPixel8bppGray:
			bpp = 1
		default:
			return errors.New("wic: 不支持的像素格式")
		}
		stride := (int(dw)*bpp + 3) &^ 3
		buf := make([]byte, stride*int(dh))
		// IWICBitmapSourceTransform::CopyPixels(prc, w, h, pguidDstFormat, dstTransform, stride, size, buf)
		if err := hrErr("CopyPixels", tr.call(3, 0, uintptr(dw), uintptr(dh), uintptr(unsafe.Pointer(&pf)), 0,
			uintptr(stride), uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))); err != nil {
			return err
		}
		out := image.NewRGBA(image.Rect(0, 0, int(dw), int(dh)))
		for y := range int(dh) {
			row, o := buf[y*stride:], y*out.Stride
			for x := range int(dw) {
				if bpp == 3 {
					out.Pix[o], out.Pix[o+1], out.Pix[o+2] = row[x*3+2], row[x*3+1], row[x*3]
				} else {
					out.Pix[o], out.Pix[o+1], out.Pix[o+2] = row[x], row[x], row[x]
				}
				out.Pix[o+3] = 255
				o += 4
			}
		}
		img = out
		return nil
	})
	return img, err
}

var heifOnce = sync.OnceValue(func() bool {
	ok := false
	withCOM(func() error {
		fac, err := wicFactory()
		if err != nil {
			return err
		}
		defer fac.release()
		var dec *comObj
		// IWICImagingFactory::CreateDecoder(guidContainerFormat, vendor, ppDecoder)
		if hrErr("CreateDecoder(HEIF)", fac.call(7, uintptr(unsafe.Pointer(&guidContainerHeif)), 0, uintptr(unsafe.Pointer(&dec)))) == nil {
			dec.release()
			ok = true
		}
		return nil
	})
	return ok
})

// HEIFDecoderInstalled 报告系统是否装有 WIC HEIF 解码器（“HEIF 图像扩展”）。
func HEIFDecoderInstalled() bool { return heifOnce() }

// over 把预乘 alpha 的通道值铺到白底上（饱和到 255）。
func over(c, a byte) byte { return byte(min(255, int(c)+255-int(a))) }
