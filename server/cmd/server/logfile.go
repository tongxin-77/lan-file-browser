package main

import (
	"os"
	"path/filepath"
)

const maxLogSize = 10 << 20 // 超过后启动时轮换为 server.log.1

// hasConsole 报告标准错误是否可用。-H windowsgui 构建双击运行时没有控制台，
// 标准句柄无效，写入会被静默丢弃。
func hasConsole() bool {
	_, err := os.Stderr.Stat()
	return err == nil
}

// openLogFile 以追加方式打开 <dataDir>\logs\server.log；超过 maxLogSize 时先改名为 server.log.1。
func openLogFile(dataDir string) (*os.File, error) {
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "server.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxLogSize {
		os.Rename(p, p+".1") // 失败则继续追加到原文件
	}
	return os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
