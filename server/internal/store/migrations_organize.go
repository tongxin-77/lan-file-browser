package store

import "database/sql"

// 自动整理记录（06 文档第 6 节），号段 200–299。
func init() {
	Register(Migration{Version: 200, Name: "organize_items", Up: func(tx *sql.Tx) error {
		// src_path 为相对源文件夹的路径（/ 分隔）；src_key = Go 侧 strings.ToLower(src_path)。
		// mtime 为 Unix 纳秒；fingerprint 为“大小:SHA-256 十六进制”；target_path 为库目录内相对路径（/ 分隔）。
		// 时间列为 Unix 毫秒。
		_, err := tx.Exec(`
CREATE TABLE organize_items (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	share_id      TEXT    NOT NULL,
	source_id     TEXT    NOT NULL,
	src_path      TEXT    NOT NULL,
	src_key       TEXT    NOT NULL,
	size          INTEGER NOT NULL,
	mtime         INTEGER NOT NULL,
	fingerprint   TEXT    NOT NULL,
	kind          TEXT    NOT NULL,
	category      TEXT    NOT NULL,
	target_path   TEXT    NOT NULL DEFAULT '',
	state         TEXT    NOT NULL,
	source_action TEXT    NOT NULL DEFAULT '',
	attempts      INTEGER NOT NULL DEFAULT 0,
	error         TEXT    NOT NULL DEFAULT '',
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL,
	done_at       INTEGER NOT NULL DEFAULT 0,
	UNIQUE (share_id, source_id, src_key, fingerprint)
);
CREATE INDEX organize_items_src ON organize_items (share_id, source_id, src_key);
CREATE INDEX organize_items_state ON organize_items (share_id, state);`)
		return err
	}})
}
