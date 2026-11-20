package organize

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// 记录状态（06 文档第 6 节）。
const (
	StateQueued    = "queued"
	StateCopying   = "copying"
	StateDone      = "done"
	StateDuplicate = "duplicate"
	StateFailed    = "failed"
)

// 源文件处理（source_action）。
const (
	ActKept          = "kept"
	ActRecycled      = "recycled"
	ActDeleted       = "deleted"
	ActRenamed       = "renamed"
	ActRecycleFailed = "recycle_failed"
)

// Item 为 organize_items 的一行。
type Item struct {
	ID           int64     `json:"id"`
	ShareID      string    `json:"-"`
	SourceID     string    `json:"sourceId"`
	SrcPath      string    `json:"srcPath"`
	SrcKey       string    `json:"-"`
	Size         int64     `json:"size"`
	Mtime        int64     `json:"-"` // Unix 纳秒
	Fingerprint  string    `json:"-"`
	Kind         string    `json:"kind"`
	Category     string    `json:"category"`
	TargetPath   string    `json:"targetPath"`
	State        string    `json:"state"`
	SourceAction string    `json:"sourceAction"`
	Attempts     int       `json:"attempts"`
	Error        string    `json:"error"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	DoneAt       time.Time `json:"doneAt"`
}

const itemCols = `id, share_id, source_id, src_path, src_key, size, mtime, fingerprint, kind, category, target_path,
	state, source_action, attempts, error, created_at, updated_at, done_at`

type scanner interface{ Scan(dest ...any) error }

func scanItem(r scanner) (*Item, error) {
	var it Item
	var c, u, d int64
	if err := r.Scan(&it.ID, &it.ShareID, &it.SourceID, &it.SrcPath, &it.SrcKey, &it.Size, &it.Mtime, &it.Fingerprint,
		&it.Kind, &it.Category, &it.TargetPath, &it.State, &it.SourceAction, &it.Attempts, &it.Error, &c, &u, &d); err != nil {
		return nil, err
	}
	it.CreatedAt, it.UpdatedAt = time.UnixMilli(c), time.UnixMilli(u)
	if d > 0 {
		it.DoneAt = time.UnixMilli(d)
	}
	return &it, nil
}

func queryItems(db *sql.DB, q string, args ...any) ([]Item, error) {
	rows, err := db.Query(`SELECT `+itemCols+` FROM organize_items `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

func queryItem(db *sql.DB, q string, args ...any) (*Item, error) {
	it, err := scanItem(db.QueryRow(`SELECT `+itemCols+` FROM organize_items `+q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return it, err
}

// latestBySrc 返回同一源文件路径最新的记录。
func latestBySrc(db *sql.DB, share, source, key string) (*Item, error) {
	return queryItem(db, `WHERE share_id = ? AND source_id = ? AND src_key = ? ORDER BY id DESC LIMIT 1`, share, source, key)
}

func byFingerprint(db *sql.DB, share, source, key, fp string) (*Item, error) {
	return queryItem(db, `WHERE share_id = ? AND source_id = ? AND src_key = ? AND fingerprint = ?`, share, source, key, fp)
}

func insertItem(db *sql.DB, it *Item, now time.Time) error {
	it.CreatedAt, it.UpdatedAt = now, now
	res, err := db.Exec(`INSERT INTO organize_items (share_id, source_id, src_path, src_key, size, mtime, fingerprint, kind,
		category, target_path, state, source_action, attempts, error, created_at, updated_at, done_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		it.ShareID, it.SourceID, it.SrcPath, it.SrcKey, it.Size, it.Mtime, it.Fingerprint, it.Kind, it.Category,
		it.TargetPath, it.State, it.SourceAction, it.Attempts, it.Error, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	it.ID, err = res.LastInsertId()
	return err
}

// saveItem 写回可变字段。
func saveItem(db *sql.DB, it *Item, now time.Time) error {
	it.UpdatedAt = now
	var done int64
	if !it.DoneAt.IsZero() {
		done = it.DoneAt.UnixMilli()
	}
	_, err := db.Exec(`UPDATE organize_items SET src_path = ?, size = ?, mtime = ?, target_path = ?, state = ?, source_action = ?,
		attempts = ?, error = ?, updated_at = ?, done_at = ? WHERE id = ?`,
		it.SrcPath, it.Size, it.Mtime, it.TargetPath, it.State, it.SourceAction, it.Attempts, it.Error, now.UnixMilli(), done, it.ID)
	return err
}

// DeleteItem 删除一条记录（“重新整理”：下次扫描按新文件处理）。
func DeleteItem(db *sql.DB, share string, id int64) (bool, error) {
	res, err := db.Exec(`DELETE FROM organize_items WHERE share_id = ? AND id = ?`, share, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// pendingItems 返回待复制的记录：排队中，或失败且未达重试上限。
func pendingItems(db *sql.DB, share string, maxRetries int) ([]Item, error) {
	return queryItems(db, `WHERE share_id = ? AND (state = 'queued' OR state = 'failed' AND attempts < ?) ORDER BY id`, share, maxRetries)
}

// doneVideos 返回某源已整理（含重复）的视频记录，供字幕跟随。
func doneVideos(db *sql.DB, share, source string) ([]Item, error) {
	return queryItems(db, `WHERE share_id = ? AND source_id = ? AND kind = 'video' AND state IN ('done', 'duplicate') ORDER BY id`, share, source)
}

// Counts 为各状态计数（06 文档第 11 节）。
type Counts struct {
	Done      int `json:"done"`
	Duplicate int `json:"duplicate"`
	Queued    int `json:"queued"`
	Watching  int `json:"watching"`
	Failed    int `json:"failed"`
}

func (c *Counts) add(state string, n int) {
	switch state {
	case StateDone:
		c.Done += n
	case StateDuplicate:
		c.Duplicate += n
	case StateQueued, StateCopying:
		c.Queued += n
	case StateFailed:
		c.Failed += n
	}
}

func countItems(db *sql.DB, share string) (Counts, map[string]*Counts, error) {
	var total Counts
	by := map[string]*Counts{}
	rows, err := db.Query(`SELECT source_id, state, COUNT(*) FROM organize_items WHERE share_id = ? GROUP BY source_id, state`, share)
	if err != nil {
		return total, by, err
	}
	defer rows.Close()
	for rows.Next() {
		var src, state string
		var n int
		if err := rows.Scan(&src, &state, &n); err != nil {
			return total, by, err
		}
		total.add(state, n)
		if by[src] == nil {
			by[src] = &Counts{}
		}
		by[src].add(state, n)
	}
	return total, by, rows.Err()
}

// ListItems 分页列出记录；state 为空时列全部，按更新时间倒序。
func ListItems(db *sql.DB, share, state string, offset, limit int) ([]Item, int, error) {
	where, args := `WHERE share_id = ?`, []any{share}
	if state != "" {
		where += ` AND state = ?`
		args = append(args, state)
	}
	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM organize_items `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	items, err := queryItems(db, where+` ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if items == nil {
		items = []Item{}
	}
	return items, total, err
}

// RetryItems 把失败记录清零重试次数后放回排队。
func RetryItems(db *sql.DB, share string, ids []int64, now time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := []any{now.UnixMilli(), share}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := db.Exec(`UPDATE organize_items SET state = 'queued', attempts = 0, error = '', updated_at = ?
		WHERE share_id = ? AND state = 'failed' AND id IN (?`+strings.Repeat(`, ?`, len(ids)-1)+`)`, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// resetCopying 把重启前未完成的 copying 记录改回 queued。
func resetCopying(db *sql.DB) error {
	_, err := db.Exec(`UPDATE organize_items SET state = 'queued' WHERE state = 'copying'`)
	return err
}
