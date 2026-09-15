// Package store holds all state in one SQLite file.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the SQLite database. One write connection and a small read pool
// avoid SQLITE_BUSY under WAL. Code inside a Tx callback must use the
// transaction, not the store: the single write connection is in use.
type Store struct {
	path string
	w    *sql.DB
	r    *sql.DB
}

// Open opens or creates the database file. It does not migrate; call
// Migrate next so that the caller can act on schema problems.
func Open(path string) (*Store, error) {
	w, err := openDB(path, false)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	r, err := openDB(path, true)
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(4)
	s := &Store{path: path, w: w, r: r}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.PingContext(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return s, nil
}

// uriEscaper escapes the characters that end or decode inside a SQLite
// file: URI path.
var uriEscaper = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23")

func openDB(path string, readOnly bool) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "busy_timeout(5000)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	}
	dsn := "file:" + uriEscaper.Replace(filepath.ToSlash(path)) + "?" + q.Encode()
	return sql.Open("sqlite", dsn)
}

// Close closes both connection pools.
func (s *Store) Close() error {
	err1 := s.r.Close()
	err2 := s.w.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// Read returns the read pool. Its connections reject writes.
func (s *Store) Read() *sql.DB { return s.r }

// Tx runs fn in a write transaction and commits when fn returns nil.
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
