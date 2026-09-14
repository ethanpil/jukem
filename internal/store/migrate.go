package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration is one numbered SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// ErrSchemaTooNew reports a database written by a newer binary. An old binary
// must never write to a newer schema.
type ErrSchemaTooNew struct {
	Database int
	Binary   int
}

func (e *ErrSchemaTooNew) Error() string {
	return fmt.Sprintf("database schema version %d is newer than this binary supports (%d)", e.Database, e.Binary)
}

// MigrationError reports a migration that failed and was rolled back.
type MigrationError struct {
	Version  int
	Snapshot string
	Err      error
}

func (e *MigrationError) Error() string {
	return fmt.Sprintf("migration %d failed: %v (snapshot: %s)", e.Version, e.Err, e.Snapshot)
}

func (e *MigrationError) Unwrap() error { return e.Err }

// Migrations returns the embedded migrations in order.
func Migrations() ([]Migration, error) {
	return loadMigrations(migrationFiles)
}

func loadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []Migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: name must be <number>_<name>.sql", name)
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		data, err := fs.ReadFile(fsys, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, Migration{Version: v, Name: name, SQL: string(data)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	for i, m := range ms {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration %s: expected version %d", m.Name, i+1)
		}
	}
	return ms, nil
}

// Version returns the schema version, 0 for an empty database.
func (s *Store) Version(ctx context.Context) (int, error) {
	var exists int
	err := s.w.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&exists)
	if err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, nil
	}
	var v int
	err = s.w.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM schema_version`).Scan(&v)
	return v, err
}

// Migrate brings the database to the newest embedded schema. Before the
// first change it copies the database to snapshotDir. Each migration runs in
// its own transaction, so a failure leaves the database at the last version
// that completed.
func (s *Store) Migrate(ctx context.Context, snapshotDir string) error {
	ms, err := Migrations()
	if err != nil {
		return err
	}
	return s.migrate(ctx, snapshotDir, ms)
}

func (s *Store) migrate(ctx context.Context, snapshotDir string, ms []Migration) error {
	current, err := s.Version(ctx)
	if err != nil {
		return err
	}
	latest := len(ms)
	if current > latest {
		return &ErrSchemaTooNew{Database: current, Binary: latest}
	}
	if current == latest {
		return nil
	}
	snapshot := ""
	if current > 0 {
		snapshot, err = s.Snapshot(ctx, snapshotDir, current)
		if err != nil {
			return fmt.Errorf("snapshot before migration: %w", err)
		}
	}
	for _, m := range ms[current:] {
		if err := s.applyMigration(ctx, m); err != nil {
			return &MigrationError{Version: m.Version, Snapshot: snapshot, Err: err}
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, m Migration) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, m.Version)
		return err
	})
}

// keepSnapshots is the number of snapshots that survive pruning.
const keepSnapshots = 5

// Snapshot copies the database with VACUUM INTO and prunes old copies. It
// returns the path of the new file.
func (s *Store) Snapshot(ctx context.Context, dir string, version int) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	name := fmt.Sprintf("jukem-v%d-%s.db", version, time.Now().UTC().Format("20060102T150405Z"))
	path := filepath.Join(dir, name)
	quoted := strings.ReplaceAll(filepath.ToSlash(path), "'", "''")
	if _, err := s.w.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return "", err
	}
	if err := pruneSnapshots(dir); err != nil {
		return path, err
	}
	return path, nil
}

// pruneSnapshots keeps the newest keepSnapshots files. The timestamp in the
// name sorts them.
func pruneSnapshots(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "jukem-v") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return stamp(names[i]) < stamp(names[j]) })
	var errs []error
	for len(names) > keepSnapshots {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			errs = append(errs, err)
		}
		names = names[1:]
	}
	return errors.Join(errs...)
}

// stamp returns the timestamp part of a snapshot name.
func stamp(name string) string {
	i := strings.LastIndex(name, "-")
	if i < 0 {
		return name
	}
	return name[i+1:]
}
