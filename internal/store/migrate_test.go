package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "jukem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func TestMigrateFreshDatabase(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	if err := s.Migrate(ctx, filepath.Join(dir, "snapshots")); err != nil {
		t.Fatal(err)
	}
	ms, _ := Migrations()
	v, err := s.Version(ctx)
	if err != nil || v != len(ms) {
		t.Fatalf("version %d err %v, want %d", v, err, len(ms))
	}
	// A fresh database takes no snapshot: there is nothing to keep.
	if _, err := os.Stat(filepath.Join(dir, "snapshots")); !os.IsNotExist(err) {
		t.Fatalf("unexpected snapshot dir: %v", err)
	}
	// A second run is a no-op.
	if err := s.Migrate(ctx, filepath.Join(dir, "snapshots")); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaTooNew(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	if err := s.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (999)`); err != nil {
		t.Fatal(err)
	}
	err := s.Migrate(ctx, dir)
	var tooNew *ErrSchemaTooNew
	if !errors.As(err, &tooNew) || tooNew.Database != 999 {
		t.Fatalf("got %v", err)
	}
}

func TestFailedMigrationRollsBackAndSnapshots(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	snapDir := filepath.Join(dir, "snapshots")
	ms := []Migration{
		{Version: 1, Name: "0001_a.sql", SQL: `CREATE TABLE a (x INTEGER);`},
		{Version: 2, Name: "0002_b.sql", SQL: `CREATE TABLE b (x INTEGER);`},
		{Version: 3, Name: "0003_bad.sql", SQL: `CREATE TABLE c (x INTEGER); CREATE TABLE b (x INTEGER);`},
	}
	if err := s.migrate(ctx, snapDir, ms[:1]); err != nil {
		t.Fatal(err)
	}
	err := s.migrate(ctx, snapDir, ms)
	var me *MigrationError
	if !errors.As(err, &me) || me.Version != 3 {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(me.Snapshot); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	v, _ := s.Version(ctx)
	if v != 2 {
		t.Fatalf("version after failure = %d, want 2", v)
	}
	// Table c from the failed migration must not exist.
	var n int
	s.w.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='c'`).Scan(&n)
	if n != 0 {
		t.Fatal("failed migration left table c")
	}
	// The snapshot is a working database at version 1.
	snap, err := Open(me.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if v, _ := snap.Version(ctx); v != 1 {
		t.Fatalf("snapshot version %d, want 1", v)
	}
}

func TestSnapshotPruning(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	if err := s.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	snapDir := filepath.Join(dir, "snapshots")
	os.MkdirAll(snapDir, 0o750)
	for _, n := range []string{"jukem-v1-20200101T000000Z.db", "jukem-v1-20200102T000000Z.db", "jukem-v1-20200103T000000Z.db", "jukem-v1-20200104T000000Z.db", "jukem-v1-20200105T000000Z.db", "other.txt"} {
		os.WriteFile(filepath.Join(snapDir, n), []byte("x"), 0o600)
	}
	if _, err := s.Snapshot(ctx, snapDir, 1); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(snapDir)
	var dbs int
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".db" {
			dbs++
		}
	}
	if dbs != keepSnapshots {
		t.Fatalf("kept %d snapshots, want %d", dbs, keepSnapshots)
	}
	if _, err := os.Stat(filepath.Join(snapDir, "jukem-v1-20200101T000000Z.db")); !os.IsNotExist(err) {
		t.Fatal("oldest snapshot should be pruned")
	}
	if _, err := os.Stat(filepath.Join(snapDir, "other.txt")); err != nil {
		t.Fatal("unrelated file must stay")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	if err := s.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	set, err := s.LoadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if set.VolumeMax != 100 || set.MusicRoot != "/srv/jukem/music" {
		t.Fatalf("defaults: %+v", set)
	}
	set.VolumeMax = 80
	set.AllowedExtensions = []string{".MP3", "flac"}
	if err := s.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	got, _ := s.LoadSettings(ctx)
	if got.VolumeMax != 80 || got.AllowedExtensions[0] != "mp3" {
		t.Fatalf("got %+v", got)
	}
	if !got.ExtensionAllowed("song.MP3") || got.ExtensionAllowed("song.exe") {
		t.Fatal("extension check")
	}
	set.VolumeMin = 90
	if err := s.SaveSettings(ctx, set); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestStateRoundTrip(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	if err := s.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	var v struct{ N int }
	ok, err := s.GetState(ctx, "x", &v)
	if err != nil || ok {
		t.Fatalf("missing key: ok=%v err=%v", ok, err)
	}
	v.N = 7
	if err := s.SetState(ctx, "x", v); err != nil {
		t.Fatal(err)
	}
	v.N = 0
	ok, _ = s.GetState(ctx, "x", &v)
	if !ok || v.N != 7 {
		t.Fatalf("got ok=%v v=%+v", ok, v)
	}
	if err := s.DeleteState(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	ok, _ = s.GetState(ctx, "x", &v)
	if ok {
		t.Fatal("key should be gone")
	}
}
