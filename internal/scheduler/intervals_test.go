package scheduler

import (
	"testing"
	"time"

	"jukem/internal/store"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skip("time zone data not available:", err)
	}
	return loc
}

func rule(id int64, name string, days int, start, end string) store.Schedule {
	return store.Schedule{ID: id, Name: name, Enabled: true, Days: days, StartTime: start, EndTime: end, SourceType: "directory", SourceRef: "Music"}
}

func TestMidnightCrossingRule(t *testing.T) {
	loc := mustLoc(t, "Europe/London")
	// Friday 22:00 to 02:00 is a Friday rule.
	r := rule(1, "Late", store.Friday, "22:00", "02:00")
	from := time.Date(2026, 1, 5, 0, 0, 0, 0, loc) // Monday
	ivs := Expand([]store.Schedule{r}, nil, loc, from, from.AddDate(0, 0, 7))
	if len(ivs) != 1 {
		t.Fatalf("got %d intervals: %+v", len(ivs), ivs)
	}
	iv := ivs[0]
	if iv.Start.In(loc).Format("Mon 15:04") != "Fri 22:00" || iv.End.In(loc).Format("Mon 15:04") != "Sat 02:00" {
		t.Fatalf("got %s to %s", iv.Start.In(loc), iv.End.In(loc))
	}
	if iv.End.Sub(iv.Start) != 4*time.Hour {
		t.Fatal(iv.End.Sub(iv.Start))
	}
}

func TestSpringForwardGapStartsAtTransition(t *testing.T) {
	loc := mustLoc(t, "Europe/London")
	// 2026-03-29 01:00 GMT jumps to 02:00 BST: 01:30 does not exist.
	r := rule(1, "Gap", store.AllDays, "01:30", "03:00")
	from := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)
	ivs := Expand([]store.Schedule{r}, nil, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 {
		t.Fatalf("got %+v", ivs)
	}
	want := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC) // the transition instant
	if !ivs[0].Start.Equal(want) {
		t.Fatalf("start %s, want %s", ivs[0].Start.UTC(), want)
	}
	// The window runs an hour shorter in real time: 02:00 BST to 03:00 BST.
	if ivs[0].End.Sub(ivs[0].Start) != time.Hour {
		t.Fatal(ivs[0].End.Sub(ivs[0].Start))
	}
}

func TestFallBackUsesFirstOccurrence(t *testing.T) {
	loc := mustLoc(t, "Europe/London")
	// 2026-10-25 02:00 BST falls back to 01:00 GMT: 01:30 happens twice.
	r := rule(1, "Twice", store.AllDays, "00:30", "01:30")
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	ivs := Expand([]store.Schedule{r}, nil, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 {
		t.Fatalf("got %+v", ivs)
	}
	// The end is the first 01:30 (BST), so the window is one hour, not two.
	if ivs[0].End.Sub(ivs[0].Start) != time.Hour {
		t.Fatalf("window %s", ivs[0].End.Sub(ivs[0].Start))
	}
	// A window across the transition runs an hour longer in real time.
	r2 := rule(2, "Across", store.AllDays, "00:00", "03:00")
	ivs = Expand([]store.Schedule{r2}, nil, loc, from, from.AddDate(0, 0, 1))
	if ivs[0].End.Sub(ivs[0].Start) != 4*time.Hour {
		t.Fatalf("window %s", ivs[0].End.Sub(ivs[0].Start))
	}
}

func TestSouthernHemisphereTransitions(t *testing.T) {
	loc := mustLoc(t, "Australia/Sydney")
	// 2026-10-04 02:00 jumps to 03:00 in Sydney.
	r := rule(1, "Gap", store.AllDays, "02:30", "04:00")
	from := time.Date(2026, 10, 4, 0, 0, 0, 0, loc)
	ivs := Expand([]store.Schedule{r}, nil, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 || ivs[0].Start.In(loc).Hour() != 3 || ivs[0].End.Sub(ivs[0].Start) != time.Hour {
		t.Fatalf("got %+v", ivs)
	}
	// 2026-04-05 03:00 falls back to 02:00.
	r2 := rule(2, "Twice", store.AllDays, "01:00", "02:30")
	from = time.Date(2026, 4, 5, 0, 0, 0, 0, loc)
	ivs = Expand([]store.Schedule{r2}, nil, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 || ivs[0].End.Sub(ivs[0].Start) != 90*time.Minute {
		t.Fatalf("got %+v", ivs)
	}
}

func TestMonthEndOvernightWindow(t *testing.T) {
	loc := mustLoc(t, "Europe/London")
	r := rule(1, "Late", store.AllDays, "22:00", "02:00")
	from := time.Date(2026, 1, 31, 12, 0, 0, 0, loc)
	ivs := Expand([]store.Schedule{r}, nil, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 {
		t.Fatalf("got %+v", ivs)
	}
	if ivs[0].End.In(loc).Format("Jan 2 15:04") != "Feb 1 02:00" || ivs[0].End.Sub(ivs[0].Start) != 4*time.Hour {
		t.Fatalf("month end: %s to %s", ivs[0].Start.In(loc), ivs[0].End.In(loc))
	}
}

func TestMidnightTransition(t *testing.T) {
	loc := mustLoc(t, "Africa/Cairo")
	// Cairo springs forward at 00:00 on 2026-04-24: 00:30 does not exist.
	r := rule(1, "Early", store.AllDays, "00:30", "06:00")
	from := time.Date(2026, 4, 23, 12, 0, 0, 0, loc)
	ivs := Expand([]store.Schedule{r}, nil, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 {
		t.Fatalf("got %+v", ivs)
	}
	if ivs[0].Start.In(loc).Format("Jan 2 15:04") != "Apr 24 01:00" || ivs[0].End.In(loc).Format("15:04") != "06:00" {
		t.Fatalf("got %s to %s", ivs[0].Start.In(loc), ivs[0].End.In(loc))
	}
	// A window into the transition day ends at the next valid instant.
	r2 := rule(2, "Late", store.AllDays, "22:00", "00:30")
	ivs = Expand([]store.Schedule{r2}, nil, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 || ivs[0].End.In(loc).Format("Jan 2 15:04") != "Apr 24 01:00" {
		t.Fatalf("got %+v", ivs)
	}
}

func TestConflictsAcrossWeekdays(t *testing.T) {
	loc := mustLoc(t, "Europe/London")
	fri := rule(1, "Friday late", store.Friday, "22:00", "02:00")
	sat := rule(2, "Saturday early", store.Saturday, "00:00", "03:00")
	mon := rule(3, "Monday", store.Monday, "09:00", "10:00")
	now := time.Date(2026, 1, 5, 12, 0, 0, 0, loc)
	c := Conflicts([]store.Schedule{fri, sat, mon}, loc, now)
	if len(c) != 1 || c[0].RuleID != 1 || c[0].OtherID != 2 {
		t.Fatalf("got %+v", c)
	}
	if c := Conflicts([]store.Schedule{fri, mon}, loc, now); len(c) != 0 {
		t.Fatalf("false conflict %+v", c)
	}
	// Two windows that touch do not overlap.
	a := rule(4, "A", store.Monday, "09:00", "10:00")
	b := rule(5, "B", store.Monday, "10:00", "11:00")
	if c := Conflicts([]store.Schedule{a, b}, loc, now); len(c) != 0 {
		t.Fatalf("touching windows conflict %+v", c)
	}
}

func TestExceptionsClipAndReplace(t *testing.T) {
	loc := mustLoc(t, "Europe/London")
	fri := rule(1, "Friday late", store.Friday, "22:00", "02:00")
	daily := rule(2, "Daily", store.AllDays, "09:00", "17:00")
	// Saturday 2026-01-10 is closed.
	silent := store.Exception{Date: "2026-01-10", Kind: "silent", Note: "Closed"}
	from := time.Date(2026, 1, 9, 0, 0, 0, 0, loc)
	ivs := Expand([]store.Schedule{fri, daily}, []store.Exception{silent}, loc, from, from.AddDate(0, 0, 2))
	var late *Interval
	for i := range ivs {
		if ivs[i].RuleID == 1 {
			late = &ivs[i]
		}
		if ivs[i].RuleID == 2 && ivs[i].Start.In(loc).Weekday() == time.Saturday {
			t.Fatal("rule ran on the silent day")
		}
	}
	if late == nil || late.End.In(loc).Format("Mon 15:04") != "Sat 00:00" {
		t.Fatalf("Friday window not clipped: %+v", late)
	}

	// Sunday has its own hours from 00:00 and a different source.
	src, ref := "playlist", "7"
	st, en := "00:00", "06:00"
	hours := store.Exception{Date: "2026-01-11", Kind: "hours", StartTime: &st, EndTime: &en, SourceType: &src, SourceRef: &ref}
	from = time.Date(2026, 1, 11, 0, 0, 0, 0, loc)
	ivs = Expand([]store.Schedule{daily}, []store.Exception{hours}, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 || ivs[0].Source.Ref != "7" || ivs[0].Start.In(loc).Hour() != 0 || ivs[0].Exception != "2026-01-11" {
		t.Fatalf("got %+v", ivs)
	}

	// A source exception keeps the normal hours with another source.
	source := store.Exception{Date: "2026-01-12", Kind: "source", SourceType: &src, SourceRef: &ref}
	from = time.Date(2026, 1, 12, 0, 0, 0, 0, loc)
	ivs = Expand([]store.Schedule{daily}, []store.Exception{source}, loc, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 || ivs[0].Source.Ref != "7" || ivs[0].Start.In(loc).Hour() != 9 || ivs[0].RuleID != 2 {
		t.Fatalf("got %+v", ivs)
	}
}

func TestCurrentAndNextBoundary(t *testing.T) {
	loc := mustLoc(t, "Europe/London")
	daily := rule(1, "Daily", store.AllDays, "09:00", "17:00")
	from := time.Date(2026, 1, 5, 0, 0, 0, 0, loc)
	ivs := Expand([]store.Schedule{daily}, nil, loc, from, from.AddDate(0, 0, 2))
	at := time.Date(2026, 1, 5, 12, 0, 0, 0, loc)
	cur, ok := Current(ivs, at)
	if !ok || cur.Name != "Daily" {
		t.Fatal("no current interval")
	}
	next, ok := NextBoundary(ivs, at)
	if !ok || next.In(loc).Format("15:04") != "17:00" {
		t.Fatalf("next %v", next)
	}
	if _, ok := Current(ivs, time.Date(2026, 1, 5, 18, 0, 0, 0, loc)); ok {
		t.Fatal("interval outside hours")
	}
	// Disabled rules do not expand.
	daily.Enabled = false
	if ivs := Expand([]store.Schedule{daily}, nil, loc, from, from.AddDate(0, 0, 2)); len(ivs) != 0 {
		t.Fatal("disabled rule expanded")
	}
}
