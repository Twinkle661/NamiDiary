package sqlitex

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"sync"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

var once sync.Once

func Open(path string) (*sql.DB, error) {
	once.Do(sqlitevec.Auto)
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = absolute
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String()+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if err = db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
