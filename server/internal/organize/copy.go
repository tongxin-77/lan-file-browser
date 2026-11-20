package organize

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"runtime"
	"strings"
	"time"
)

const (
	chunkSize   = 1 << 20 // 1 MiB 分块（06 文档第 7 节）
	fpEdge      = 1 << 20 // 快速指纹取前后各 1 MiB
	tempPrefix  = ".lfb-copying-"
	bytesPerMiB = 1 << 20
)

// fingerprint 计算快速指纹：“大小:SHA-256(前 1 MiB ‖ 后 1 MiB)”，小于 2 MiB 时对整个文件做哈希（06 文档第 2 节）。
func fingerprint(r io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	if size < 2*fpEdge {
		if _, err := io.Copy(h, io.NewSectionReader(r, 0, size)); err != nil {
			return "", err
		}
	} else {
		if _, err := io.Copy(h, io.NewSectionReader(r, 0, fpEdge)); err != nil {
			return "", err
		}
		if _, err := io.Copy(h, io.NewSectionReader(r, size-fpEdge, fpEdge)); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%d:%s", size, hex.EncodeToString(h.Sum(nil))), nil
}

// limiter 为按块取令牌的限速器（06 文档第 8 节）：累计字节数按速率折算出最早可写时刻，未到则睡眠。
type limiter struct {
	rate  float64 // 字节/秒，<= 0 不限
	start time.Time
	n     int64
}

func (l *limiter) wait(ctx context.Context, c Clock, n int) error {
	if l.rate <= 0 {
		return nil
	}
	now := c.Now()
	if l.start.IsZero() {
		l.start = now
	}
	l.n += int64(n)
	due := l.start.Add(time.Duration(float64(l.n) / l.rate * float64(time.Second)))
	if d := due.Sub(now); d > 0 {
		return c.Sleep(ctx, d)
	}
	return nil
}

// reset 在暂停恢复后重新计时，避免把暂停时间当作“欠下的额度”突发写入。
func (l *limiter) reset() { l.start, l.n = time.Time{}, 0 }

var errChanged = errors.New("复制期间源文件被改动")

// copyJob 描述一次复制：从已打开的源文件写到库目录 root 内 dir 下的临时文件。
type copyJob struct {
	src      *os.File
	size     int64
	fp       string
	mtime    time.Time
	root     *os.Root
	dir      string                          // 目标目录（库内相对，/ 分隔）
	gate     func(ctx context.Context) error // 每块之前调用：暂停等待 + 限速
	progress func(done int64)
}

// copyToTemp 复制到 dir/.lfb-copying-<随机>，Sync 后校验大小与指纹、设置 mtime，返回临时文件名（库内相对路径）。
// 失败（含 ctx 取消）时删除临时文件。
func copyToTemp(ctx context.Context, j copyJob) (tmp string, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer backgroundIO()()

	b := make([]byte, 8)
	rand.Read(b)
	tmp = path.Join(j.dir, tempPrefix+hex.EncodeToString(b))
	out, err := j.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer func() {
		if out != nil {
			out.Close()
		}
		if err != nil {
			j.root.Remove(tmp)
			tmp = ""
		}
	}()
	buf := make([]byte, chunkSize)
	var done int64
	for {
		if err := j.gate(ctx); err != nil {
			return tmp, err
		}
		n, rerr := j.src.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return tmp, err
			}
			done += int64(n)
			if j.progress != nil {
				j.progress(done)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return tmp, rerr
		}
	}
	if err := out.Sync(); err != nil {
		return tmp, err
	}
	err = out.Close()
	out = nil
	if err != nil {
		return tmp, err
	}
	if err := verify(j.root, tmp, j.size, j.fp); err != nil {
		return tmp, err
	}
	return tmp, j.root.Chtimes(tmp, j.mtime, j.mtime)
}

// verify 校验库内文件的大小与快速指纹。
func verify(root *os.Root, name string, size int64, fp string) error {
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() != size {
		return errors.New("校验失败：大小不一致")
	}
	got, err := fingerprint(f, size)
	if err != nil {
		return err
	}
	if got != fp {
		return errors.New("校验失败：内容不一致")
	}
	return nil
}

// numbered 返回 name 的第 i 个重名候选：`E02.mkv` → `E02 (1).mkv`（03 §4.9）。
func numbered(name string, i int) string {
	if i == 0 {
		return name
	}
	ext := path.Ext(name)
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), i, ext)
}

// chooseName 在 dir 下为 name 选最终名：同名且大小、指纹相同 → dup 为 true（重复）；不同 → 加 (1)、(2)…
func chooseName(root *os.Root, dir, name string, size int64, fp string) (final string, dup bool, err error) {
	for i := 0; i < 10000; i++ {
		cand := path.Join(dir, numbered(name, i))
		fi, err := root.Lstat(cand)
		if errors.Is(err, fs.ErrNotExist) {
			return cand, false, nil
		}
		if err != nil {
			return "", false, err
		}
		if fi.Mode().IsRegular() && fi.Size() == size && verify(root, cand, size, fp) == nil {
			return cand, true, nil
		}
	}
	return "", false, errors.New("重名过多")
}
