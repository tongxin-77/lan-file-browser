// Package store 打开服务端 SQLite 数据库 <数据目录>\data.db（modernc.org/sqlite，纯 Go），
// 开启 WAL，并执行版本化迁移。
//
// 加表方式：每个功能在本包新建独立文件（如 migrations_progress.go），在 init 中调用 Register，
// 版本号取自己的号段（见 migrations.go），不修改已发布的迁移。
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // 注册驱动 "sqlite"
)

const fileName = "data.db"

// Open 打开（不存在则创建）dir\data.db 并执行全部已注册迁移。
func Open(dir string) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return open(filepath.Join(dir, fileName), registered())
}

func open(path string, ms []Migration) (*sql.DB, error) {
	// _pragma 对连接池中每个新连接生效
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(db, ms); err != nil {
		db.Close()
		return nil, fmt.Errorf("迁移 %s: %w", fileName, err)
	}
	return db, nil
}
