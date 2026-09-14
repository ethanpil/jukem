// Package store holds all state in one SQLite file.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the SQLite database. One write connection and a small read pool
// avoid SQLITE_BUSY under WAL.
type Store struct {
	path string
	w    *sql.DB
	r    *sql.DB
}

// Open opens or creates the database file. It does not migrate; call
// Migrate next so that the caller can act on schema problems.
func Open(path string) (*Store, error) {
	w, err := openDB(path)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	r, err := openDB(path)
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

func openDB(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "busy_timeout(5000)")
	dsn := "file:" + filepath.ToSlash(path) + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes both connection pools.
func (s *Store) Close() error {
	err1 := s.r.Close()
	err2 := s.w.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// Read returns the read pool.
func (s *Store) Read() *sql.DB { return s.r }

// Write returns the single-connection write pool.
func (s *Store) Write() *sql.DB { return s.w }

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

// now returns the current time as stored in the database.
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// parseTime reads a time written by now.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
