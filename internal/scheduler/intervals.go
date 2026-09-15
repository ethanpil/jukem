// Package scheduler expands weekly rules and date exceptions into absolute
// intervals, decides who owns playback, and keeps MPD converged on the
// desired state.
package scheduler

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"jukem/internal/store"
)

// Source is what an interval plays.
type Source struct {
	Type string `json:"type" enum:"directory,playlist"`
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
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", s)
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", s)
	}
	return h, m, nil
}

// localTime returns the instant for a wall-clock time on a date, with the
// daylight saving policy: a time that does not exist becomes the next
// valid instant, and a time that happens twice is its first occurrence.
func localTime(y int, m time.Month, d, hh, mm int, loc *time.Location) time.Time {
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, loc)
	dayEnd := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	_, off1 := dayStart.Zone()
	_, off2 := dayEnd.Zone()
	want := hh*60 + mm
	utc := time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
	var matches []time.Time
	for _, off := range []int{off1, off2} {
		cand := utc.Add(-time.Duration(off) * time.Second)
		local := cand.In(loc)
		if local.Hour()*60+local.Minute() == want && local.Day() == d {
			matches = append(matches, cand)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0]
	case 2:
		if matches[1].Before(matches[0]) {
			return matches[1]
		}
		return matches[0]
	}
	// The time falls in a gap. The next valid instant is the transition.
	return transitionBetween(dayStart, dayEnd, off1)
}

// transitionBetween finds the instant where the zone offset stops being
// off, by binary search between lo and hi.
func transitionBetween(lo, hi time.Time, off int) time.Time {
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		if _, o := mid.Zone(); o == off {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi.Truncate(time.Second)
}

// weekdayBit maps a Go weekday onto the store's Monday-first bit mask.
func weekdayBit(d time.Weekday) int {
	if d == time.Sunday {
		return store.Sunday
	}
	return 1 << (int(d) - 1)
}

// ruleWindow returns the interval of a rule on a date, or false when the
// rule does not run that day.
func ruleWindow(r store.Schedule, y int, m time.Month, d int, loc *time.Location) (time.Time, time.Time, bool) {
	date := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if r.Days&weekdayBit(date.Weekday()) == 0 {
		return time.Time{}, time.Time{}, false
	}
	return window(r.StartTime, r.EndTime, y, m, d, loc)
}

// window turns wall-clock start and end on a date into instants. An end
// that is not after the start runs past midnight.
func window(startHM, endHM string, y int, m time.Month, d int, loc *time.Location) (time.Time, time.Time, bool) {
	sh, sm, err := parseHM(startHM)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	eh, em, err := parseHM(endHM)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	start := localTime(y, m, d, sh, sm, loc)
	var end time.Time
	if eh*60+em > sh*60+sm {
		end = localTime(y, m, d, eh, em, loc)
	} else {
		end = localTime(y, m, d+1, eh, em, loc)
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
func Expand(rules []store.Schedule, exceptions []store.Exception, loc *time.Location, from, to time.Time, defaultShuffle bool) []Interval {
	excByDate := map[string]store.Exception{}
	for _, e := range exceptions {
		excByDate[e.Date] = e
	}
	var out []Interval
	first := from.In(loc).AddDate(0, 0, -1)
	last := to.In(loc).AddDate(0, 0, 1)
	for day := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc); !day.After(last); day = day.AddDate(0, 0, 1) {
		y, m, d := day.Date()
		date := day.Format("2006-01-02")
		exc, hasExc := excByDate[date]
		var dayIntervals []Interval
		for _, r := range rules {
			if !r.Enabled {
				continue
			}
			start, end, ok := ruleWindow(r, y, m, d, loc)
			if !ok {
				continue
			}
			iv := Interval{
				Start: start, End: end, RuleID: r.ID, Name: r.Name,
				Source:  Source{Type: r.SourceType, Ref: r.SourceRef},
				Options: Options{Shuffle: r.Shuffle, Volume: r.Volume},
				Key:     fmt.Sprintf("rule:%d:%d", r.ID, start.Unix()),
			}
			dayIntervals = append(dayIntervals, iv)
		}
		if hasExc {
			dayIntervals = applyException(exc, dayIntervals, y, m, d, loc)
		}
		out = append(out, dayIntervals...)
	}
	// An exception day clips what reaches into it from the day before.
	for i := range out {
		next := out[i].Start.In(loc).AddDate(0, 0, 1)
		nextDate := time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, loc)
		if _, ok := excByDate[nextDate.Format("2006-01-02")]; ok && out[i].End.After(nextDate) && out[i].Exception != nextDate.Format("2006-01-02") {
			out[i].End = nextDate
		}
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
func applyException(exc store.Exception, rules []Interval, y int, m time.Month, d int, loc *time.Location) []Interval {
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
		start, end, ok := window(*exc.StartTime, *exc.EndTime, y, m, d, loc)
		if !ok {
			return nil
		}
		iv := Interval{
			Start: start, End: end, Name: name, Exception: exc.Date,
			Source:  Source{Type: *exc.SourceType, Ref: *exc.SourceRef},
			Options: Options{Shuffle: exc.Shuffle != nil && *exc.Shuffle, Volume: exc.Volume},
			Key:     fmt.Sprintf("exc:%s:%d", exc.Date, start.Unix()),
		}
		return []Interval{iv}
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
	from := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	ivs := Expand(rules, nil, loc, from, from.AddDate(0, 0, 8), false)
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
