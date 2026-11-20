package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestOpenWALAndInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	vs, err := Versions(db)
	if err != nil || !slices.Contains(vs, 1) {
		t.Fatalf("初始迁移未应用: %v %v", vs, err)
	}
}

// 迁移幂等：重复执行不重复应用；重开数据库后版本保持；后加入的低版本号迁移也会执行。
func TestMigrateIdempotentAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	runs := map[int]int{}
	m := func(v int, sqlText string) Migration {
		return Migration{Version: v, Name: "m", Up: func(tx *sql.Tx) error {
			runs[v]++
			_, err := tx.Exec(sqlText)
			return err
		}}
	}
	ms := []Migration{m(200, `CREATE TABLE b (x)`), m(1, `CREATE TABLE a (x)`)}

	db, err := open(path, ms)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(db, ms); err != nil {
		t.Fatal(err)
	}
	db.Close()

	ms = append(ms, m(100, `CREATE TABLE c (x)`)) // 新功能后加入、版本号更低
	db, err = open(path, ms)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vs, err := Versions(db)
	if err != nil || !slices.Equal(vs, []int{1, 100, 200}) {
		t.Fatalf("版本: %v %v", vs, err)
	}
	for v, n := range runs {
		if n != 1 {
			t.Errorf("迁移 %d 执行了 %d 次", v, n)
		}
	}
	if _, err := db.Exec(`INSERT INTO c VALUES (1)`); err != nil {
		t.Fatal(err)
	}
}

// 迁移失败整体回滚，版本不记录。
func TestMigrateFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	boom := errors.New("boom")
	bad := Migration{Version: 5, Name: "bad", Up: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE half (x)`); err != nil {
			return err
		}
		return boom
	}}
	if _, err := open(path, []Migration{bad}); !errors.Is(err, boom) {
		t.Fatalf("应返回迁移错误: %v", err)
	}
	db, err := open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if vs, _ := Versions(db); len(vs) != 0 {
		t.Fatalf("失败的迁移不应记录: %v", vs)
	}
	if _, err := db.Exec(`SELECT * FROM half`); err == nil {
		t.Fatal("失败迁移建的表应已回滚")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("重复版本应 panic")
		}
	}()
	Register(Migration{Version: 1, Name: "dup", Up: func(*sql.Tx) error { return nil }})
}
