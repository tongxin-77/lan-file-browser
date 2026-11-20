package store

import (
	"database/sql"
	"fmt"
	"slices"
	"sync"
)

// 版本号段（各功能在自己的号段内递增，避免多人同时选中同一版本号）：
//
//	1         初始迁移（本文件）
//	100–199   观看进度 / 继续观看（migrations_progress.go）
//	200–299   自动整理（migrations_organize.go）
//	300–399   缩略图等其他功能
//	400–499   设备（devices.json 迁入，如需要）
//
// 已应用的版本逐条记录在 schema_version 表；启动时按版本升序执行所有未应用的迁移，
// 因此低号段的迁移晚于高号段加入也会被执行。

// Migration 为一次迁移。Up 在事务中执行，与写入 schema_version 同一事务提交。
type Migration struct {
	Version int
	Name    string
	Up      func(tx *sql.Tx) error
}

var (
	regMu    sync.Mutex
	registry []Migration
)

// Register 注册迁移，供各 migrations_*.go 的 init 调用；版本号重复或非正数时 panic。
func Register(m Migration) {
	regMu.Lock()
	defer regMu.Unlock()
	if m.Version <= 0 || m.Up == nil {
		panic(fmt.Sprintf("store: 迁移 %d %q 非法", m.Version, m.Name))
	}
	for _, x := range registry {
		if x.Version == m.Version {
			panic(fmt.Sprintf("store: 迁移版本 %d 重复（%q 与 %q）", m.Version, x.Name, m.Name))
		}
	}
	registry = append(registry, m)
}

func registered() []Migration {
	regMu.Lock()
	defer regMu.Unlock()
	return slices.Clone(registry)
}

func init() {
	Register(Migration{Version: 1, Name: "init", Up: func(*sql.Tx) error { return nil }})
}

// migrate 按版本升序执行 ms 中尚未应用的迁移；可重复调用（幂等）。
func migrate(db *sql.DB, ms []Migration) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL DEFAULT (unixepoch())
	)`); err != nil {
		return err
	}
	ms = slices.Clone(ms)
	slices.SortFunc(ms, func(a, b Migration) int { return a.Version - b.Version })
	for _, m := range ms {
		if err := apply(db, m); err != nil {
			return fmt.Errorf("版本 %d %q: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

func apply(db *sql.DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // 提交后为空操作
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = ?`, m.Version).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if err := m.Up(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version, name) VALUES (?, ?)`, m.Version, m.Name); err != nil {
		return err
	}
	return tx.Commit()
}

// Versions 返回已应用的迁移版本（升序）。
func Versions(db *sql.DB) ([]int, error) {
	rows, err := db.Query(`SELECT version FROM schema_version ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vs []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, rows.Err()
}
