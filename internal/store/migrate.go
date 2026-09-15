package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
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

// ErrSchemaTooNew reports a database that a newer binary wrote. An old
// binary must never write to a newer schema.
type ErrSchemaTooNew struct {
	Database int
	Binary   int
}

func (e *ErrSchemaTooNew) Error() string {
	return fmt.Sprintf("database schema version %d is newer than this binary supports (%d)", e.Database, e.Binary)
}

// MigrationError reports a migration that failed. The store rolled it back,
// and the database stays at the last version that completed. Snapshot is
// empty when the database was new, because there was nothing to keep.
type MigrationError struct {
	Version  int
	Snapshot string
	Err      error
}

func (e *MigrationError) Error() string {
	return fmt.Sprintf("migration %d failed: %v", e.Version, e.Err)
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
		_, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, m.Version)
		return err
	})
}

// keepSnapshots is the number of snapshots that survive pruning.
const keepSnapshots = 5

// Snapshot copies the database with VACUUM INTO and removes old copies. It
// returns the path of the new file. A copy that does not complete is
// removed, so a partial file never counts as a snapshot.
func (s *Store) Snapshot(ctx context.Context, dir string, version int) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, snapshotName(version, time.Now()))
	if _, err := s.w.ExecContext(ctx, "VACUUM INTO ?", filepath.ToSlash(path)); err != nil {
		os.Remove(path)
		return "", err
	}
	// A prune failure is not a reason to stop an upgrade: the new snapshot
	// exists, and an extra file in the directory does no harm.
	if err := pruneSnapshots(dir); err != nil {
		slog.Warn("cannot remove old database snapshots", "dir", dir, "error", err)
	}
	return path, nil
}

// snapshotName builds jukem-v<version>-<timestamp>.db. The timestamp has
// millisecond resolution so that two snapshots in one second differ.
func snapshotName(version int, t time.Time) string {
	return fmt.Sprintf("jukem-v%d-%s.db", version, t.UTC().Format("20060102T150405.000Z"))
}

// snapshotFile is a parsed snapshot name.
type snapshotFile struct {
	name    string
	version int
	stamp   string
}

// listSnapshots returns the snapshots in dir, newest first.
func listSnapshots(dir string) ([]snapshotFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []snapshotFile
	for _, e := range entries {
		var v int
		var stamp string
		rest, ok := strings.CutPrefix(e.Name(), "jukem-v")
		if !ok || !strings.HasSuffix(rest, ".db") {
			continue
		}
		vs, st, ok := strings.Cut(strings.TrimSuffix(rest, ".db"), "-")
		if !ok {
			continue
		}
		v, err := strconv.Atoi(vs)
		if err != nil {
			continue
		}
		stamp = st
		files = append(files, snapshotFile{name: e.Name(), version: v, stamp: stamp})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].stamp > files[j].stamp })
	return files, nil
}

// pruneSnapshots keeps the newest keepSnapshots files. It never removes the
// last snapshot of a schema version: after a migration that fails on every
// start, that file is the one a rollback needs.
func pruneSnapshots(dir string) error {
	files, err := listSnapshots(dir)
	if err != nil {
		return err
	}
	perVersion := map[int]int{}
	for _, f := range files {
		perVersion[f.version]++
	}
	kept := 0
	var errs []string
	for _, f := range files {
		if kept < keepSnapshots || perVersion[f.version] == 1 {
			kept++
			continue
		}
		if err := os.Remove(filepath.Join(dir, f.name)); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		perVersion[f.version]--
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// LatestSnapshot returns the newest snapshot in dir whose schema version is
// at most maxVersion. It returns false when there is none.
func LatestSnapshot(dir string, maxVersion int) (string, bool) {
	files, err := listSnapshots(dir)
	if err != nil {
		return "", false
	}
	for _, f := range files {
		if f.version <= maxVersion {
			return filepath.Join(dir, f.name), true
		}
	}
	return "", false
}
