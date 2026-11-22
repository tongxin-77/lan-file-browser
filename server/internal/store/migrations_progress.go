package store

import "database/sql"

// 观看进度（03 文档 4.8），号段 100–199。
func init() {
	Register(Migration{Version: 100, Name: "progress", Up: func(tx *sql.Tx) error {
		// path 为共享内相对路径（/ 分隔，保留请求时的大小写）；path_key = Go 侧 strings.ToLower(path)，
		// 因 SQLite NOCASE 只处理 ASCII。position/duration 单位秒；updated_at 为 Unix 毫秒。
		_, err := tx.Exec(`
CREATE TABLE progress (
	device_id  TEXT    NOT NULL,
	share_id   TEXT    NOT NULL,
	path_key   TEXT    NOT NULL,
	path       TEXT    NOT NULL,
	position   REAL    NOT NULL,
	duration   REAL    NOT NULL,
	watched    INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (device_id, share_id, path_key)
) WITHOUT ROWID;
CREATE INDEX progress_share_updated ON progress (share_id, updated_at DESC);`)
		return err
	}})
	// 继续观看「本设备移除」（03 文档 4.8）：按设备记录移除的「共享+目录」及时间，
	// 该目录有晚于 hidden_at 的进度时重新出现。share_id、dir_key 均为空串表示清空（隐藏此前全部）。
	// dir_key 为 path.Dir(progress.Key(path))，根目录为 "."。
	Register(Migration{Version: 101, Name: "continue_hidden", Up: func(tx *sql.Tx) error {
		_, err := tx.Exec(`
CREATE TABLE continue_hidden (
	device_id TEXT    NOT NULL,
	share_id  TEXT    NOT NULL,
	dir_key   TEXT    NOT NULL,
	hidden_at INTEGER NOT NULL,
	PRIMARY KEY (device_id, share_id, dir_key)
) WITHOUT ROWID;`)
		return err
	}})
}
