// Package scheduler expands weekly rules and date exceptions into absolute
// intervals, decides who owns playback, and keeps MPD converged on the
// desired state.
package scheduler

import (
	"fmt"
	"sort"
	"time"

	"jukem/internal/store"
)

// Source is what an interval plays.
type Source struct {
	Type string `json:"type" enum:"directory,playlist,stream"`
	Ref  string `json:"ref"`
}

// Options are the play options of an interval.
type Options struct {
	Shuffle bool `json:"shuffle"`
	Volume  *int `json:"volume,omitempty"`
}

// Interval is one concrete window in absolute time.
type Interval struct {
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Key       string    `json:"key" doc:"Program key: the rule or exception plus the start instant"`
	Name      string    `json:"name"`
	Source    Source    `json:"source"`
	Options   Options   `json:"options"`
	RuleID    int64     `json:"rule_id,omitempty"`
	Exception string    `json:"exception,omitempty" doc:"Date of the exception that produced or changed this interval"`
}

// Contains reports whether t is inside the interval.
func (iv Interval) Contains(t time.Time) bool {
	return !t.Before(iv.Start) && t.Before(iv.End)
}

// parseHM reads HH:MM.
func parseHM(s string) (int, int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", s)
	}
	return t.Hour(), t.Minute(), nil
}

// localTime returns the instant for a wall-clock time on a date, with the
// daylight saving policy. A time that does not exist becomes the next
// valid instant. A time that happens twice is its first occurrence.
//
// The zone offset before and after the day give the candidate instants.
// A candidate whose local clock shows the wanted time is a real
// occurrence; the earliest wins. With no such candidate the time is in a
// gap, and a search between the candidates finds the transition.
func localTime(day time.Time, hh, mm int, loc *time.Location) time.Time {
	y, m, d := day.In(loc).Date()
	want := hh*60 + mm
	naive := time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
	local := func(t time.Time) (sameDay bool, minutes int) {
		lt := t.In(loc)
		ly, lm, ld := lt.Date()
		return ly == y && lm == m && ld == d, lt.Hour()*60 + lt.Minute()
	}
	reached := func(t time.Time) bool {
		lt := t.In(loc)
		ly, lm, ld := lt.Date()
		switch {
		case ly != y:
			return ly > y
		case lm != m:
			return lm > m
		case ld != d:
			return ld > d
		}
		return lt.Hour()*60+lt.Minute() >= want
	}
	var cands []time.Time
	for _, probe := range []time.Time{naive.Add(-30 * time.Hour), naive, naive.Add(30 * time.Hour)} {
		_, off := probe.In(loc).Zone()
		c := naive.Add(-time.Duration(off) * time.Second)
		dup := false
		for _, o := range cands {
			dup = dup || o.Equal(c)
		}
		if !dup {
			cands = append(cands, c)
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Before(cands[j]) })
	for _, c := range cands {
		if ok, mins := local(c); ok && mins == want {
			return c
		}
	}
	// A gap: the transition lies between the last candidate before the
	// wanted time and the first one after it.
	lo, hi := cands[0], cands[len(cands)-1]
	for _, c := range cands {
		if !reached(c) {
			lo = c
		}
	}
	for i := len(cands) - 1; i >= 0; i-- {
		if reached(cands[i]) {
			hi = cands[i]
		}
	}
	if !lo.Before(hi) {
		return hi
	}
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		if reached(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi.Truncate(time.Second)
}

// dayAt returns midnight of the date, normalised, so d+1 on the last day
// of a month rolls over.
func dayAt(y int, m time.Month, d int, loc *time.Location) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// weekdayBit maps a Go weekday onto the store's Monday-first bit mask.
func weekdayBit(d time.Weekday) int {
	return 1 << ((int(d) + 6) % 7)
}

// window turns wall-clock start and end on a date into instants. An end
// that is not after the start runs past midnight.
func window(startHM, endHM string, day time.Time, loc *time.Location) (time.Time, time.Time, bool) {
	sh, sm, err := parseHM(startHM)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	eh, em, err := parseHM(endHM)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	start := localTime(day, sh, sm, loc)
	var end time.Time
	if eh*60+em > sh*60+sm {
		end = localTime(day, eh, em, loc)
	} else {
		y, m, d := day.In(loc).Date()
		end = localTime(dayAt(y, m, d+1, loc), eh, em, loc)
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// Expand turns rules and exceptions into intervals for every occurrence
// that touches [from, to). Exceptions govern their whole calendar day:
// rule intervals that start on the day are replaced, and intervals from
// the day before are clipped at midnight.
func Expand(rules []store.Schedule, exceptions []store.Exception, loc *time.Location, from, to time.Time) []Interval {
	excByDate := map[string]store.Exception{}
	for _, e := range exceptions {
		excByDate[e.Date] = e
	}
	var out []Interval
	fy, fm, fd := from.In(loc).Date()
	ly, lm, ld := to.In(loc).Date()
	last := dayAt(ly, lm, ld+1, loc)
	for day := dayAt(fy, fm, fd-1, loc); !day.After(last); day = dayAt(day.Year(), day.Month(), day.Day()+1, loc) {
		date := day.Format("2006-01-02")
		bit := weekdayBit(day.Weekday())
		var dayIntervals []Interval
		for _, r := range rules {
			if !r.Enabled || r.Days&bit == 0 {
				continue
			}
			start, end, ok := window(r.StartTime, r.EndTime, day, loc)
			if !ok {
				continue
			}
			dayIntervals = append(dayIntervals, Interval{
				Start: start, End: end, RuleID: r.ID, Name: r.Name,
				Source:  Source{Type: r.SourceType, Ref: r.SourceRef},
				Options: Options{Shuffle: r.Shuffle, Volume: r.Volume},
				Key:     fmt.Sprintf("rule:%d:%d", r.ID, start.Unix()),
			})
		}
		if exc, ok := excByDate[date]; ok {
			dayIntervals = applyException(exc, dayIntervals, day, loc)
		}
		// The next day's exception clips what reaches into it.
		next := dayAt(day.Year(), day.Month(), day.Day()+1, loc)
		if _, ok := excByDate[next.Format("2006-01-02")]; ok {
			for i := range dayIntervals {
				if dayIntervals[i].End.After(next) {
					dayIntervals[i].End = next
				}
			}
		}
		out = append(out, dayIntervals...)
	}
	// Keep only what touches the window, in start order.
	kept := out[:0]
	for _, iv := range out {
		if iv.End.After(from) && iv.Start.Before(to) && iv.End.After(iv.Start) {
			kept = append(kept, iv)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Start.Before(kept[j].Start) })
	return kept
}

// applyException replaces or changes the rule intervals of one day.
func applyException(exc store.Exception, rules []Interval, day time.Time, loc *time.Location) []Interval {
	name := exc.Note
	if name == "" {
		name = "Exception " + exc.Date
	}
	switch exc.Kind {
	case "silent":
		return nil
	case "hours":
		if exc.StartTime == nil || exc.EndTime == nil || exc.SourceType == nil || exc.SourceRef == nil {
			return nil
		}
		start, end, ok := window(*exc.StartTime, *exc.EndTime, day, loc)
		if !ok {
			return nil
		}
		return []Interval{{
			Start: start, End: end, Name: name, Exception: exc.Date,
			Source:  Source{Type: *exc.SourceType, Ref: *exc.SourceRef},
			Options: Options{Shuffle: exc.Shuffle != nil && *exc.Shuffle, Volume: exc.Volume},
			Key:     fmt.Sprintf("exc:%s:%d", exc.Date, start.Unix()),
		}}
	case "source":
		out := make([]Interval, 0, len(rules))
		for _, iv := range rules {
			if exc.SourceType != nil && exc.SourceRef != nil {
				iv.Source = Source{Type: *exc.SourceType, Ref: *exc.SourceRef}
			}
			if exc.Shuffle != nil {
				iv.Options.Shuffle = *exc.Shuffle
			}
			if exc.Volume != nil {
				iv.Options.Volume = exc.Volume
			}
			iv.Name = name
			iv.Exception = exc.Date
			iv.Key = fmt.Sprintf("exc:%s:%d", exc.Date, iv.Start.Unix())
			out = append(out, iv)
		}
		return out
	}
	return rules
}

// Conflict is a pair of rules whose intervals overlap.
type Conflict struct {
	RuleID    int64  `json:"rule_id"`
	RuleName  string `json:"rule_name"`
	OtherID   int64  `json:"other_id"`
	OtherName string `json:"other_name"`
	At        string `json:"at" doc:"When the overlap first happens, in local time"`
}

// Conflicts expands the rules over one full week and reports every pair
// that overlaps. Exceptions are not rules and do not count.
func Conflicts(rules []store.Schedule, loc *time.Location, now time.Time) []Conflict {
	y, m, d := now.In(loc).Date()
	from := dayAt(y, m, d, loc)
	ivs := Expand(rules, nil, loc, from, dayAt(y, m, d+8, loc))
	seen := map[[2]int64]bool{}
	var out []Conflict
	for i := 0; i < len(ivs); i++ {
		for j := i + 1; j < len(ivs); j++ {
			if !ivs[j].Start.Before(ivs[i].End) {
				break
			}
			if ivs[i].RuleID == ivs[j].RuleID {
				continue
			}
			pair := [2]int64{min(ivs[i].RuleID, ivs[j].RuleID), max(ivs[i].RuleID, ivs[j].RuleID)}
			if seen[pair] {
				continue
			}
			seen[pair] = true
			out = append(out, Conflict{
				RuleID: ivs[i].RuleID, RuleName: ivs[i].Name, OtherID: ivs[j].RuleID, OtherName: ivs[j].Name,
				At: ivs[j].Start.In(loc).Format("Mon 15:04"),
			})
		}
	}
	return out
}

// Current returns the interval that contains t, if any.
func Current(ivs []Interval, t time.Time) (Interval, bool) {
	for _, iv := range ivs {
		if iv.Contains(t) {
			return iv, true
		}
	}
	return Interval{}, false
}

// NextBoundary returns the first interval start or end after t, if any.
func NextBoundary(ivs []Interval, t time.Time) (time.Time, bool) {
	var best time.Time
	for _, iv := range ivs {
		for _, b := range []time.Time{iv.Start, iv.End} {
			if b.After(t) && (best.IsZero() || b.Before(best)) {
				best = b
			}
		}
	}
	return best, !best.IsZero()
}
