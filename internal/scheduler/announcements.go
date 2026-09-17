package scheduler

import (
	"time"

	"jukem/internal/store"
)

// AnnouncementGrace is how late an announcement may still play. A machine
// that was off, or busy, does not play an old announcement when it returns.
const AnnouncementGrace = 2 * time.Minute

// AnnouncementDue reports the time an announcement should play for, when it
// is due now. It is due when its local day is one of its days, its time has
// passed by less than the grace, and it has not played for that time yet.
func AnnouncementDue(a store.Announcement, now time.Time, loc *time.Location) (time.Time, bool) {
	if !a.Enabled {
		return time.Time{}, false
	}
	local := now.In(loc)
	if a.Days&weekdayBit(local.Weekday()) == 0 {
		return time.Time{}, false
	}
	slot, ok := lastSlot(a, local, loc)
	if !ok {
		return time.Time{}, false
	}
	if local.Sub(slot) > AnnouncementGrace {
		return time.Time{}, false
	}
	if a.LastPlayed != nil && !a.LastPlayed.Before(slot) {
		return time.Time{}, false
	}
	return slot, true
}

// lastSlot returns the newest play time of the local day that is not after
// local.
func lastSlot(a store.Announcement, local time.Time, loc *time.Location) (time.Time, bool) {
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	switch a.Mode {
	case "at":
		if a.AtTime == nil {
			return time.Time{}, false
		}
		at, ok := atTime(day, *a.AtTime)
		if !ok || local.Before(at) {
			return time.Time{}, false
		}
		return at, true
	case "every":
		if a.StartTime == nil || a.EndTime == nil || a.EveryMinutes == nil || *a.EveryMinutes <= 0 {
			return time.Time{}, false
		}
		start, ok1 := atTime(day, *a.StartTime)
		end, ok2 := atTime(day, *a.EndTime)
		if !ok1 || !ok2 || !end.After(start) || local.Before(start) {
			return time.Time{}, false
		}
		step := time.Duration(*a.EveryMinutes) * time.Minute
		// The slots are start, start+step, ... and none is after end.
		n := int(local.Sub(start) / step)
		slot := start.Add(time.Duration(n) * step)
		if slot.After(end) {
			return time.Time{}, false
		}
		return slot, true
	}
	return time.Time{}, false
}

// atTime puts an HH:MM wall-clock time on a local day.
func atTime(day time.Time, hhmm string) (time.Time, bool) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, day.Location()), true
}

// NextAnnouncements returns the next play time after now, in the coming
// week, and the announcements that play at that time. ok is false when no
// announcement plays in the coming week.
func NextAnnouncements(list []store.Announcement, now time.Time, loc *time.Location) (time.Time, []store.Announcement, bool) {
	var first time.Time
	var group []store.Announcement
	for _, a := range list {
		// A time that is due now, and did not play yet, comes first. It
		// plays on one of the next checks.
		t, ok := AnnouncementDue(a, now, loc)
		if !ok {
			t, ok = nextSlot(a, now, loc)
		}
		switch {
		case !ok:
		case group == nil || t.Before(first):
			first, group = t, []store.Announcement{a}
		case t.Equal(first):
			group = append(group, a)
		}
	}
	return first, group, group != nil
}

// nextSlot returns the first play time of an announcement after now. It
// looks at today and the seven days after it.
func nextSlot(a store.Announcement, now time.Time, loc *time.Location) (time.Time, bool) {
	if !a.Enabled {
		return time.Time{}, false
	}
	local := now.In(loc)
	for d := range 8 {
		day := time.Date(local.Year(), local.Month(), local.Day()+d, 0, 0, 0, 0, loc)
		if a.Days&weekdayBit(day.Weekday()) == 0 {
			continue
		}
		switch a.Mode {
		case "at":
			if a.AtTime == nil {
				return time.Time{}, false
			}
			if t, ok := atTime(day, *a.AtTime); ok && t.After(now) {
				return t, true
			}
		case "every":
			if a.StartTime == nil || a.EndTime == nil || a.EveryMinutes == nil || *a.EveryMinutes <= 0 {
				return time.Time{}, false
			}
			start, ok1 := atTime(day, *a.StartTime)
			end, ok2 := atTime(day, *a.EndTime)
			if !ok1 || !ok2 || !end.After(start) {
				return time.Time{}, false
			}
			slot := start
			if !start.After(now) {
				// The slots are start, start+step, ... and none is after end.
				step := time.Duration(*a.EveryMinutes) * time.Minute
				slot = start.Add((now.Sub(start)/step + 1) * step)
			}
			if !slot.After(end) {
				return slot, true
			}
		default:
			return time.Time{}, false
		}
	}
	return time.Time{}, false
}
