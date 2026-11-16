//go:build cgo

// Package sqlite provides a deliberately small SQLite wrapper without a Go module download.
// It requires CGO_ENABLED=1 and a system libsqlite3 development package.
package sqlite

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
static int bind_copy(sqlite3_stmt *stmt, int index, const char *s) {
    return sqlite3_bind_text(stmt, index, s, -1, SQLITE_TRANSIENT);
}
*/
import "C"
import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"unsafe"
)

type DB struct {
	mu   sync.Mutex
	conn *C.sqlite3
}

type Row map[string]string

type Tx struct{ db *DB }

func Open(path string) (*DB, error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var conn *C.sqlite3
	rc := C.sqlite3_open_v2(cp, &conn, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_CREATE|C.SQLITE_OPEN_FULLMUTEX, nil)
	if rc != C.SQLITE_OK {
		msg := "sqlite open failed"
		if conn != nil {
			msg = C.GoString(C.sqlite3_errmsg(conn))
			C.sqlite3_close(conn)
		}
		return nil, errors.New(msg)
	}
	db := &DB{conn: conn}
	if err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.conn == nil {
		return nil
	}
	rc := C.sqlite3_close(db.conn)
	if rc != C.SQLITE_OK {
		return db.err()
	}
	db.conn = nil
	return nil
}
func (db *DB) err() error { return errors.New(C.GoString(C.sqlite3_errmsg(db.conn))) }
func (db *DB) Exec(q string, args ...any) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.run(q, args...)
	return err
}
func (db *DB) Query(q string, args ...any) ([]Row, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.run(q, args...)
}
func (db *DB) Transaction(fn func(*Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, err := db.run("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	if err := fn(&Tx{db}); err != nil {
		_, _ = db.run("ROLLBACK")
		return err
	}
	if _, err := db.run("COMMIT"); err != nil {
		_, _ = db.run("ROLLBACK")
		return err
	}
	return nil
}
func (tx *Tx) Exec(q string, args ...any) error           { _, err := tx.db.run(q, args...); return err }
func (tx *Tx) Query(q string, args ...any) ([]Row, error) { return tx.db.run(q, args...) }
func (db *DB) run(q string, args ...any) ([]Row, error) {
	cq := C.CString(q)
	defer C.free(unsafe.Pointer(cq))
	var stmt *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(db.conn, cq, -1, &stmt, nil); rc != C.SQLITE_OK {
		return nil, db.err()
	}
	defer C.sqlite3_finalize(stmt)
	for i, arg := range args {
		v := fmt.Sprint(arg)
		cv := C.CString(v)
		rc := C.bind_copy(stmt, C.int(i+1), cv)
		C.free(unsafe.Pointer(cv))
		if rc != C.SQLITE_OK {
			return nil, db.err()
		}
	}
	var rows []Row
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			return rows, nil
		}
		if rc != C.SQLITE_ROW {
			return nil, db.err()
		}
		row := Row{}
		for i := C.int(0); i < C.sqlite3_column_count(stmt); i++ {
			key := C.GoString(C.sqlite3_column_name(stmt, i))
			if C.sqlite3_column_type(stmt, i) == C.SQLITE_NULL {
				row[key] = ""
				continue
			}
			row[key] = C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, i))))
		}
		rows = append(rows, row)
	}
}
func Int(row Row, key string) int64 { n, _ := strconv.ParseInt(row[key], 10, 64); return n }
