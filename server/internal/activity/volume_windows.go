package activity

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// VolumeOf 返回 path 所在卷的挂载点（如 `D:\`，或挂载文件夹路径），供按卷统计播放；失败时返回盘符前缀。
// 同一物理盘的判定（磁盘号）由调用方（整理库）按卷再查。
func VolumeOf(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err == nil {
		buf := make([]uint16, windows.MAX_LONG_PATH)
		if err = windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err == nil {
			return windows.UTF16ToString(buf)
		}
	}
	return filepath.VolumeName(path) + `\`
}
