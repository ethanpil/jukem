package scheduler

import (
	"testing"
	"time"

	"jukem/internal/store"
)

func ptrS(s string) *string { return &s }
func ptrI(i int) *int       { return &i }

func at(loc *time.Location, day int, hh, mm int) time.Time {
	return time.Date(2026, 9, day, hh, mm, 0, 0, loc)
}

// Monday 2026-09-14, Tuesday 2026-09-15.
func TestAnnouncementDueAtTime(t *testing.T) {
	loc := time.UTC
	a := store.Announcement{Enabled: true, Days: store.Monday, Mode: "at", AtTime: ptrS("10:15")}

	if _, ok := AnnouncementDue(a, at(loc, 14, 10, 14), loc); ok {
		t.Fatal("before its time")
	}
	slot, ok := AnnouncementDue(a, at(loc, 14, 10, 15), loc)
	if !ok || slot != at(loc, 14, 10, 15) {
		t.Fatalf("at its time: %v %v", slot, ok)
	}
	if _, ok := AnnouncementDue(a, at(loc, 14, 10, 18), loc); ok {
		t.Fatal("later than the grace")
	}
	if _, ok := AnnouncementDue(a, at(loc, 15, 10, 15), loc); ok {
		t.Fatal("another day")
	}
	played := at(loc, 14, 10, 15)
	a.LastPlayed = &played
	if _, ok := AnnouncementDue(a, at(loc, 14, 10, 16), loc); ok {
		t.Fatal("played for this time already")
	}
}

func TestAnnouncementDueEvery(t *testing.T) {
	loc := time.UTC
	a := store.Announcement{Enabled: true, Days: store.AllDays, Mode: "every",
		StartTime: ptrS("09:00"), EndTime: ptrS("10:00"), EveryMinutes: ptrI(20)}

	for _, tc := range []struct {
		hh, mm int
		want   bool
	}{
		{8, 59, false},
		{9, 0, true},
		{9, 1, true},    // inside the grace of the 09:00 slot
		{9, 5, false},   // past the grace, before the next slot
		{9, 20, true},   // second slot
		{9, 40, true},   // third slot
		{10, 0, true},   // the last slot is the end itself
		{10, 20, false}, // after the end
	} {
		_, ok := AnnouncementDue(a, at(loc, 14, tc.hh, tc.mm), loc)
		if ok != tc.want {
			t.Errorf("%02d:%02d: due %v, want %v", tc.hh, tc.mm, ok, tc.want)
		}
	}
}

func TestAnnouncementDueDisabledOrIncomplete(t *testing.T) {
	loc := time.UTC
	off := store.Announcement{Enabled: false, Days: store.AllDays, Mode: "at", AtTime: ptrS("10:15")}
	if _, ok := AnnouncementDue(off, at(loc, 14, 10, 15), loc); ok {
		t.Fatal("an announcement that is off never plays")
	}
	noTime := store.Announcement{Enabled: true, Days: store.AllDays, Mode: "at"}
	if _, ok := AnnouncementDue(noTime, at(loc, 14, 10, 15), loc); ok {
		t.Fatal("no time, no play")
	}
	badWindow := store.Announcement{Enabled: true, Days: store.AllDays, Mode: "every",
		StartTime: ptrS("10:00"), EndTime: ptrS("09:00"), EveryMinutes: ptrI(15)}
	if _, ok := AnnouncementDue(badWindow, at(loc, 14, 10, 0), loc); ok {
		t.Fatal("an end before the start is no window")
	}
}

// A zone with daylight saving must not repeat or skip a play.
func TestAnnouncementDueZone(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zone database")
	}
	a := store.Announcement{Enabled: true, Days: store.AllDays, Mode: "at", AtTime: ptrS("10:15")}
	utc := time.Date(2026, 9, 14, 14, 15, 0, 0, time.UTC) // 10:15 in New York
	if _, ok := AnnouncementDue(a, utc, loc); !ok {
		t.Fatal("due in the configured zone")
	}
	if _, ok := AnnouncementDue(a, utc, time.UTC); ok {
		t.Fatal("not due in UTC")
	}
}
