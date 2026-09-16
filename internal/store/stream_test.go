package store

import (
	"context"
	"path/filepath"
	"testing"
)

// A rule and an exception must accept a stream source after the migration
// that widened the check.
func TestStreamSourceIsStored(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "jukem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}

	id, err := db.CreateSchedule(ctx, Schedule{
		Name: "Evening radio", Enabled: true, Days: AllDays, StartTime: "18:00", EndTime: "22:00",
		SourceType: "stream", SourceRef: "https://stream.example.com/live.mp3",
	})
	if err != nil {
		t.Fatalf("a rule with a stream: %v", err)
	}
	got, ok, err := db.GetSchedule(ctx, id)
	if err != nil || !ok {
		t.Fatalf("read it back: %v %v", ok, err)
	}
	if got.SourceType != "stream" || got.SourceRef != "https://stream.example.com/live.mp3" {
		t.Fatalf("stored rule: %+v", got)
	}

	typ, ref := "stream", "https://stream.example.com/other.mp3"
	if err := db.PutException(ctx, Exception{Date: "2026-12-24", Kind: "source", SourceType: &typ, SourceRef: &ref}); err != nil {
		t.Fatalf("an exception with a stream: %v", err)
	}

	// The old source types still work.
	if _, err := db.CreateSchedule(ctx, Schedule{
		Name: "Opening hours", Enabled: true, Days: Monday, StartTime: "09:00", EndTime: "17:00",
		SourceType: "directory", SourceRef: "",
	}); err != nil {
		t.Fatalf("a rule with a folder: %v", err)
	}
	if _, err := db.CreateSchedule(ctx, Schedule{
		Name: "Bad", Enabled: true, Days: Monday, StartTime: "09:00", EndTime: "17:00",
		SourceType: "tape", SourceRef: "",
	}); err == nil {
		t.Fatal("an unknown source type must be refused")
	}
}
