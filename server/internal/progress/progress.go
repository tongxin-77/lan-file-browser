// Package progress 读写观看进度（03 文档 4.8），表由 internal/store 的迁移 100 创建。
//
// 路径一律为共享内相对路径（/ 分隔）；path_key = strings.ToLower(path)，用于大小写不敏感匹配
// （SQLite NOCASE 只处理 ASCII）。调用方负责校验共享可见性与文件存在。
package progress

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Record 为一条进度记录；Position、Duration 单位秒。
type Record struct {
	Device    string
	Share     string
	Path      string
	Position  float64
	Duration  float64
	Watched   bool
	UpdatedAt time.Time
}

// Entry 为列表中单个视频的进度。
type Entry struct {
	Position float64
	Duration float64
	Watched  bool
}

// Watched 为看完判定：position ≥ 90% × duration，或片长 ≥ 10 分钟且 duration − position < 120 秒
// （短视频只按 90% 判定，避免刚开始播放就算看完）；时长未知（≤0）为 false。
func Watched(position, duration float64) bool {
	return duration > 0 && (position >= 0.9*duration || duration >= 600 && duration-position < 120)
}

// Key 返回路径的匹配键（反斜杠转 /，转小写）。
func Key(path string) string {
	return strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
}

// Save 写入（覆盖）设备对某文件的进度；Watched 按 Watched() 重新计算。
func Save(ctx context.Context, db *sql.DB, r Record) error {
	p := strings.ReplaceAll(r.Path, `\`, "/")
	_, err := db.ExecContext(ctx, `
INSERT INTO progress (device_id, share_id, path_key, path, position, duration, watched, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (device_id, share_id, path_key) DO UPDATE SET
	path = excluded.path, position = excluded.position, duration = excluded.duration,
	watched = excluded.watched, updated_at = excluded.updated_at`,
		r.Device, r.Share, Key(p), p, r.Position, r.Duration, Watched(r.Position, r.Duration), r.UpdatedAt.UnixMilli())
	return err
}

// InDir 一次查询返回设备在共享目录 dir（"" 为根）下直接子文件的进度，键为小写文件名。
func InDir(ctx context.Context, db *sql.DB, device, share, dir string) (map[string]Entry, error) {
	prefix := Key(dir)
	if prefix != "" {
		prefix += "/"
	}
	q := `SELECT path_key, position, duration, watched FROM progress WHERE device_id = ? AND share_id = ?`
	args := []any{device, share}
	if prefix != "" {
		// 按主键范围扫描 [prefix, prefix 末字节 + 1)：'/' 的下一个字节是 '0'
		q += ` AND path_key >= ? AND path_key < ?`
		args = append(args, prefix, prefix[:len(prefix)-1]+"0")
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]Entry{}
	for rows.Next() {
		var key string
		var e Entry
		if err := rows.Scan(&key, &e.Position, &e.Duration, &e.Watched); err != nil {
			return nil, err
		}
		name := key[len(prefix):]
		if !strings.Contains(name, "/") { // 跳过更深层子目录
			m[name] = e
		}
	}
	return m, rows.Err()
}

// Recent 按更新时间倒序遍历 shares 内所有设备的记录，fn 返回 false 时停止。
func Recent(ctx context.Context, db *sql.DB, shares []string, fn func(Record) bool) error {
	if len(shares) == 0 {
		return nil
	}
	q := `SELECT device_id, share_id, path, position, duration, watched, updated_at FROM progress
WHERE share_id IN (?` + strings.Repeat(",?", len(shares)-1) + `) ORDER BY updated_at DESC, device_id`
	args := make([]any, len(shares))
	for i, s := range shares {
		args[i] = s
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r Record
		var ms int64
		if err := rows.Scan(&r.Device, &r.Share, &r.Path, &r.Position, &r.Duration, &r.Watched, &ms); err != nil {
			return err
		}
		r.UpdatedAt = time.UnixMilli(ms)
		if !fn(r) {
			return nil
		}
	}
	return rows.Err()
}
