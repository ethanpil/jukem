package library

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jukem/internal/events"
	"jukem/internal/store"
)

func newTestPlaylists(t *testing.T) (*Playlists, *Files, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "jukem.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root := filepath.Join(dir, "music")
	os.MkdirAll(filepath.Join(root, "Rock", "Band"), 0o750)
	os.WriteFile(filepath.Join(root, "Rock", "Band", "a.mp3"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(root, "Rock", "Band", "b.mp3"), []byte("b"), 0o644)
	rootFn := func() string { return root }
	p := NewPlaylists(filepath.Join(dir, "playlists"), rootFn, st)
	lim := Limits{MaxBytes: 1000, Extension: func(string) bool { return true }}
	f := NewFiles(rootFn, func() Limits { return lim }, nil, events.New(), slog.New(slog.DiscardHandler), "host")
	return p, f, root
}

func TestPlaylistLifecycle(t *testing.T) {
	p, _, _ := newTestPlaylists(t)
	ctx := context.Background()
	pl, err := p.Create(ctx, "Morning Mix")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(ctx, "Morning Mix"); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := p.Create(ctx, "../evil"); err == nil {
		t.Fatal("bad name accepted")
	}
	if n, err := p.Append(ctx, pl, []string{"Rock/Band/a.mp3", "Rock/Band/gone.mp3"}); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	entries, err := p.EntriesWithState(pl.Name)
	if err != nil || len(entries) != 2 || entries[0].Missing || !entries[1].Missing {
		t.Fatalf("got %+v %v", entries, err)
	}
	data, _ := os.ReadFile(p.file(pl.Name))
	if !strings.HasPrefix(string(data), "#EXTM3U\n") || !strings.Contains(string(data), "Rock/Band/a.mp3\n") {
		t.Fatalf("m3u content %q", data)
	}
	if err := p.Rename(ctx, pl, "Evening"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.file("Evening")); err != nil {
		t.Fatal("file not renamed")
	}
	pl.Name = "Evening"
	if err := p.Set(ctx, pl, []string{"Rock/Band/b.mp3"}); err != nil {
		t.Fatal(err)
	}
	if e, _ := p.Entries("Evening"); len(e) != 1 || e[0] != "Rock/Band/b.mp3" {
		t.Fatalf("set: %v", e)
	}
	if err := p.Delete(ctx, pl); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.file("Evening")); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}
}

func TestMoveUpdatesReferences(t *testing.T) {
	p, f, root := newTestPlaylists(t)
	ctx := context.Background()
	pl, _ := p.Create(ctx, "Mix")
	p.Set(ctx, pl, []string{"Rock/Band/a.mp3", "Rock/Band/b.mp3", "Other/x.mp3"})
	st := p.store
	st.CreateSchedule(ctx, store.Schedule{Name: "Rock hour", Enabled: true, Days: store.AllDays, StartTime: "09:00", EndTime: "10:00", SourceType: "directory", SourceRef: "Rock/Band"})
	st.AddDoNotPlay(ctx, "Rock/Band/b.mp3", "B")
	refs := References{Playlists: p, Store: st}

	ins, err := f.Inspect(ctx, refs, []string{"Rock/Band"})
	if err != nil || ins.Files != 2 || ins.Folders != 1 || len(ins.Playlists) != 1 || len(ins.Schedules) != 1 {
		t.Fatalf("inspect %+v %v", ins, err)
	}

	if err := f.Move(ctx, refs, "Rock/Band", "Pop/TheBand"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Pop", "TheBand", "a.mp3")); err != nil {
		t.Fatal("folder not moved")
	}
	entries, _ := p.Entries("Mix")
	if entries[0] != "Pop/TheBand/a.mp3" || entries[1] != "Pop/TheBand/b.mp3" || entries[2] != "Other/x.mp3" {
		t.Fatalf("playlist not rewritten: %v", entries)
	}
	rules, _ := st.ListSchedules(ctx)
	if rules[0].SourceRef != "Pop/TheBand" {
		t.Fatalf("schedule not repointed: %+v", rules[0])
	}
	dnp, _ := st.DoNotPlaySet(ctx)
	if !dnp["Pop/TheBand/b.mp3"] || dnp["Rock/Band/b.mp3"] {
		t.Fatalf("do-not-play not moved: %v", dnp)
	}
	f.timer.Stop()

	// A rename of one file.
	if err := f.Move(ctx, refs, "Pop/TheBand/a.mp3", "Pop/TheBand/first.mp3"); err != nil {
		t.Fatal(err)
	}
	entries, _ = p.Entries("Mix")
	if entries[0] != "Pop/TheBand/first.mp3" {
		t.Fatalf("file rename not applied: %v", entries)
	}
	if err := f.Move(ctx, refs, "Pop", "Pop/Inside"); err == nil {
		t.Fatal("moved a folder into itself")
	}

	// Delete drops the entries and the do-not-play row.
	if err := f.Delete(ctx, refs, []string{"Pop/TheBand"}); err != nil {
		t.Fatal(err)
	}
	entries, _ = p.Entries("Mix")
	if len(entries) != 1 || entries[0] != "Other/x.mp3" {
		t.Fatalf("delete did not drop entries: %v", entries)
	}
	if dnp, _ := st.DoNotPlaySet(ctx); len(dnp) != 0 {
		t.Fatalf("do-not-play not cleaned: %v", dnp)
	}
	f.timer.Stop()
}
