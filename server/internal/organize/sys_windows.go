package organize

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	fileFlagSequentialScan = 0x08000000
	fileFlagOpenReparse    = 0x00200000
	fileFlagBackupSem      = 0x02000000
	ioctlVolumeDiskExtents = 0x00560000
	threadModeBgBegin      = 0x00010000
	threadModeBgEnd        = 0x00020000
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procSetThreadPriority    = kernel32.NewProc("SetThreadPriority")
	shell32                  = windows.NewLazySystemDLL("shell32.dll")
	procSHFileOperationW     = shell32.NewProc("SHFileOperationW")
	errLocked                = errors.New("文件被其他程序占用")
	errRecycleBinUnsupported = errors.New("所在硬盘没有回收站")

	// 供其他包（如 /api/delete）判断 ShellRecycler.Recycle 的错误。
	ErrInUse        = errLocked
	ErrNoRecycleBin = errRecycleBinUnsupported
)

// openSourceRead 以“拒绝写共享”（只允许别人读）打开源文件：不跟随链接、顺序读提示（06 文档 4.2 #3、第 8 节）。
// 其他进程持有写句柄时返回 errLocked。
func openSourceRead(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|fileFlagSequentialScan|fileFlagOpenReparse, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errLocked
		}
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("不是普通文件")
	}
	return os.NewFile(uintptr(h), path), nil
}

// longPath 为超过 MAX_PATH 的绝对路径加 \\?\ 前缀。
func longPath(p string) string {
	if len(p) < 248 || strings.HasPrefix(p, `\\?\`) {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}

// realPath 打开目录（跟随链接/联接）并返回最终真实路径（06 文档第 9 节“真实路径规范化”）。
func realPath(p string) (string, error) {
	u, err := windows.UTF16PtrFromString(longPath(filepath.Clean(p)))
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(u, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, fileFlagBackupSem, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", err
	}
	s := windows.UTF16ToString(buf[:n])
	switch {
	case strings.HasPrefix(s, `\\?\UNC\`):
		s = `\\` + s[8:]
	case strings.HasPrefix(s, `\\?\`):
		s = s[4:]
	}
	return s, nil
}

// volumeName 返回 path 所在卷的 GUID 名（\\?\Volume{…}\），失败时返回卷挂载点。
func volumeName(path string) string {
	mp := volumeMount(path)
	u, err := windows.UTF16PtrFromString(mp)
	if err != nil {
		return strings.ToLower(mp)
	}
	buf := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(u, &buf[0], uint32(len(buf))); err != nil {
		return strings.ToLower(mp)
	}
	return strings.ToLower(windows.UTF16ToString(buf))
}

func volumeMount(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err == nil {
		buf := make([]uint16, windows.MAX_LONG_PATH)
		if err = windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err == nil {
			return windows.UTF16ToString(buf)
		}
	}
	return filepath.VolumeName(path) + `\`
}

// diskKey 返回 path 所在物理磁盘的标识（"disk:N[,M]"，按 IOCTL_VOLUME_GET_VOLUME_DISK_EXTENTS）；
// 取不到时退化为卷标识（"vol:…"），即只把同一卷视为同盘。
func diskKey(path string) string {
	vol := volumeName(path)
	dev := strings.TrimSuffix(vol, `\`)
	if strings.HasPrefix(dev, `\\?\`) {
		dev = `\\.\` + dev[4:]
	}
	u, err := windows.UTF16PtrFromString(dev)
	if err != nil {
		return "vol:" + vol
	}
	h, err := windows.CreateFile(u, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return "vol:" + vol
	}
	defer windows.CloseHandle(h)
	// VOLUME_DISK_EXTENTS：DWORD 个数 + 对齐 + 每项 {DWORD DiskNumber; LARGE_INTEGER Start; LARGE_INTEGER Length}（24 字节）
	buf := make([]byte, 8+24*16)
	var n uint32
	if err := windows.DeviceIoControl(h, ioctlVolumeDiskExtents, nil, 0, &buf[0], uint32(len(buf)), &n, nil); err != nil || n < 8 {
		return "vol:" + vol
	}
	count := *(*uint32)(unsafe.Pointer(&buf[0]))
	var parts []string
	for i := uint32(0); i < count && 8+24*(i+1) <= n; i++ {
		parts = append(parts, fmt.Sprint(*(*uint32)(unsafe.Pointer(&buf[8+24*i]))))
	}
	if len(parts) == 0 {
		return "vol:" + vol
	}
	return "disk:" + strings.Join(parts, ",")
}

// sameDisk 报告两个磁盘标识是否涉及同一块物理盘。
func sameDisk(a, b string) bool {
	if a == b {
		return true
	}
	pa, oka := strings.CutPrefix(a, "disk:")
	pb, okb := strings.CutPrefix(b, "disk:")
	if !oka || !okb {
		return false
	}
	for _, x := range strings.Split(pa, ",") {
		for _, y := range strings.Split(pb, ",") {
			if x == y {
				return true
			}
		}
	}
	return false
}

// freeSpace 返回 path 所在盘的可用与总容量（字节）。
func freeSpace(path string) (free, total uint64, err error) {
	u, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var all uint64
	err = windows.GetDiskFreeSpaceEx(u, &free, &total, &all)
	return free, total, err
}

// moveFile 同卷重命名，目标已存在时失败（MoveFileEx 不带 REPLACE_EXISTING）。
func moveFile(from, to string) error {
	f, err := windows.UTF16PtrFromString(longPath(from))
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(longPath(to))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(f, t, 0)
}

// backgroundIO 把当前 OS 线程的 IO 与内存优先级降为后台级（06 文档第 8 节），返回恢复函数。
// 调用方须已 runtime.LockOSThread。
func backgroundIO() (end func()) {
	th := uintptr(windows.CurrentThread())
	if r, _, _ := procSetThreadPriority.Call(th, threadModeBgBegin); r == 0 {
		return func() {}
	}
	return func() { procSetThreadPriority.Call(th, threadModeBgEnd) }
}

// Recycler 把文件移入回收站；测试注入假实现。
type Recycler interface {
	Recycle(path string) error
}

// ShellRecycler 用 SHFileOperationW（FO_DELETE + FOF_ALLOWUNDO）移入回收站。
// 先确认所在盘有回收站且放得下（recycleBinFits）：否则返回错误、不删除（SHFileOperation 会静默永久删除）。
type ShellRecycler struct{}

type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// recycleBinFits 报告文件能否进回收站：SHFileOperation 在没有回收站（U 盘、网络盘）、该盘设置了
// “不移到回收站”（NukeOnDelete）或文件超过回收站容量时会静默永久删除，须事先排除。
// 不用 SHQueryRecycleBinW：它要遍历回收站全部项目，项目多时需数秒。
func recycleBinFits(path string, size int64) bool {
	mount := volumeMount(path)
	u, err := windows.UTF16PtrFromString(mount)
	if err != nil || windows.GetDriveType(u) != windows.DRIVE_FIXED {
		return false
	}
	vol := volumeName(path)
	i, j := strings.Index(vol, "{"), strings.Index(vol, "}")
	if i < 0 || j < i {
		return true // 取不到卷 GUID：按默认设置（有回收站）处理
	}
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`+vol[i:j+1], registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("NukeOnDelete"); err == nil && v != 0 {
		return false
	}
	if mb, _, err := k.GetIntegerValue("MaxCapacity"); err == nil && size > int64(mb)<<20 {
		return false
	}
	return true
}

const (
	foDelete          = 3
	fofSilent         = 0x0004
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040
	fofNoErrorUI      = 0x0400
)

// inUse 报告 path（文件或文件夹本身）是否被其他程序以不允许删除的方式打开（如播放器）：
// 以 DELETE 访问、全共享方式试开，共享冲突即占用。
func inUse(path string) bool {
	u, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(u, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, fileFlagBackupSem|fileFlagOpenReparse, 0)
	if err != nil {
		return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
	}
	windows.CloseHandle(h)
	return false
}

func (ShellRecycler) Recycle(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("回收站需要绝对路径")
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if inUse(path) {
		return errLocked
	}
	size := fi.Size()
	if fi.IsDir() { // 文件夹按内部文件总大小判断回收站容量
		size = 0
		filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if i, err := d.Info(); err == nil {
					size += i.Size()
				}
			}
			return nil
		})
	}
	if !recycleBinFits(path, size) {
		return errRecycleBinUnsupported
	}
	from, err := syscall.UTF16FromString(path)
	if err != nil {
		return err
	}
	from = append(from, 0) // 双 NUL 结尾
	op := shFileOpStruct{wFunc: foDelete, pFrom: &from[0], fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI}
	if r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op))); r != 0 || op.fAnyOperationsAborted != 0 {
		if r == uintptr(windows.ERROR_SHARING_VIOLATION) || r == uintptr(windows.ERROR_LOCK_VIOLATION) {
			return errLocked // 如文件夹内有文件被占用
		}
		return fmt.Errorf("移入回收站失败（%#x）", r)
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("移入回收站后文件仍在")
	}
	return nil
}
