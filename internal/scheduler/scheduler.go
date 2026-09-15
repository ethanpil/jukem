package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"jukem/internal/events"
	"jukem/internal/player"
	"jukem/internal/store"
)

// Player is what the reconciler needs from the player.
type Player interface {
	Status() (player.Status, error)
	Load(files []string, shuffle bool, volume int) (int, error)
	Play() error
	Stop() error
	Pause() error
	SetVolumeRaw(v int) error
	Finished(st player.Status) bool
	Generation() int64
}

// Resolver turns a source into an ordered list of files. truncated is true
// when the source held more than the queue ceiling.
type Resolver func(ctx context.Context, src Source) (files []string, truncated bool, err error)

// Deps are the collaborators of the scheduler.
type Deps struct {
	Store         *store.Store
	Player        Player
	Events        *events.Hub
	Clock         *Clock
	Log           *slog.Logger
	Settings      func() store.Settings
	Resolve       Resolver
	MPDRunning    func() bool
	DevicePresent func() bool
	// OnProblem is told about repeated play failures and dead air, for
	// the watchdog. It may be nil.
	OnProblem func(kind, message string)
	// OnProgram is told when a program starts, for history. It may be nil.
	OnProgram func(name string)
}

// Program is what jukem loaded into MPD: the key of the occurrence, the
// source and options, and the queue generation at the load.
type Program struct {
	Key        string  `json:"key"`
	Name       string  `json:"name"`
	Source     Source  `json:"source"`
	Options    Options `json:"options"`
	Generation int64   `json:"generation"`
}

const programKey = "program"

// tickInterval is how often the loop reconciles without an event.
const tickInterval = 5 * time.Second

// Scheduler converges MPD on the desired state.
type Scheduler struct {
	d    Deps
	kick chan struct{}

	mu       sync.Mutex
	ivs      []Interval
	ivsAt    time.Time
	ivsStale bool
	loaded   Program
	fade     *fadeState
	failures int
	// restoreVolume is the level before a fade-out, for the next start.
	restoreVolume int
	// lastKey avoids a log line and an event on every tick.
	lastOwner player.Owner
}

// fadeState is a fade in progress. The loop only checks that its reason
// still holds while it runs.
type fadeState struct {
	kind   string // "out" or "in"
	key    string // the program the fade is for
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a scheduler and loads the persisted program.
func New(ctx context.Context, d Deps) *Scheduler {
	if d.OnProblem == nil {
		d.OnProblem = func(string, string) {}
	}
	if d.OnProgram == nil {
		d.OnProgram = func(string) {}
	}
	s := &Scheduler{d: d, kick: make(chan struct{}, 1), ivsStale: true}
	var p Program
	if ok, err := d.Store.GetState(ctx, programKey, &p); err == nil && ok {
		s.loaded = p
	}
	return s
}

// Now returns the time the scheduler works with.
func (s *Scheduler) Now() time.Time { return s.d.Clock.Now() }

// Kick asks the loop to reconcile now.
func (s *Scheduler) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Invalidate drops the interval cache after a rule, exception or zone
// change, and reconciles.
func (s *Scheduler) Invalidate() {
	s.mu.Lock()
	s.ivsStale = true
	s.mu.Unlock()
	s.Kick()
}

// Run reconciles every tickInterval and after every kick until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.cancelFade()
			return
		case <-tick.C:
		case <-s.kick:
		}
		s.Tick(ctx)
	}
}

// Intervals returns the expanded intervals for a range, from the cache
// when it covers the range.
func (s *Scheduler) Intervals(ctx context.Context, from, to time.Time) ([]Interval, error) {
	set := s.d.Settings()
	loc, err := time.LoadLocation(set.TimeZone)
	if err != nil {
		return nil, err
	}
	rules, err := s.d.Store.ListSchedules(ctx)
	if err != nil {
		return nil, err
	}
	excs, err := s.d.Store.ListExceptions(ctx, from.In(loc).AddDate(0, 0, -1).Format("2006-01-02"), to.In(loc).AddDate(0, 0, 1).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	return Expand(rules, excs, loc, from, to, set.DefaultShuffle), nil
}

// current returns the cached rolling window, refreshed when stale or once
// an hour, so that a new day rolls in.
func (s *Scheduler) current(ctx context.Context, now time.Time) []Interval {
	s.mu.Lock()
	if !s.ivsStale && now.Sub(s.ivsAt) < time.Hour {
		ivs := s.ivs
		s.mu.Unlock()
		return ivs
	}
	s.mu.Unlock()
	ivs, err := s.Intervals(ctx, now.AddDate(0, 0, -1), now.AddDate(0, 0, 8))
	if err != nil {
		s.d.Log.Warn("cannot expand the schedule", "error", err)
		return nil
	}
	s.mu.Lock()
	s.ivs, s.ivsAt, s.ivsStale = ivs, now, false
	s.mu.Unlock()
	return ivs
}

// Owner reports who decides playback now, with the reason in plain words.
func (s *Scheduler) Owner(ctx context.Context) player.Owner {
	now := s.d.Clock.Now()
	set := s.d.Settings()
	loc, _ := time.LoadLocation(set.TimeZone)
	if loc == nil {
		loc = time.UTC
	}
	if !s.d.MPDRunning() {
		return player.Owner{State: player.OwnerUnavailable, Reason: "MPD is not running"}
	}
	warning := ""
	if !s.d.DevicePresent() {
		warning = "Output device missing"
	} else if !s.d.Clock.Trusted(ctx) {
		warning = "Clock not set"
	}
	if !set.SchedulerEnabled {
		return player.Owner{State: player.OwnerManual, Reason: "Scheduler off", Warning: warning}
	}
	if warning != "" {
		return player.Owner{State: player.OwnerUnavailable, Reason: warning}
	}
	ivs := s.current(ctx, now)
	if o, ok := s.activeOverride(ctx, now, ivs); ok {
		return s.overrideOwner(o, now, ivs, loc)
	}
	if iv, ok := Current(ivs, now); ok {
		return player.Owner{State: player.OwnerScheduled, Reason: fmt.Sprintf("%s until %s", iv.Name, fmtWhen(iv.End, now, loc)), Program: iv.Name, Until: &iv.End}
	}
	for _, iv := range ivs {
		if iv.Start.After(now) {
			return player.Owner{State: player.OwnerScheduled, Reason: "Nothing scheduled until " + fmtWhen(iv.Start, now, loc), Until: &iv.Start}
		}
	}
	return player.Owner{State: player.OwnerScheduled, Reason: "Nothing scheduled this week"}
}

func (s *Scheduler) overrideOwner(o store.Override, now time.Time, ivs []Interval, loc *time.Location) player.Owner {
	what := map[string]string{"play": "Playing", "pause": "Paused", "stop": "Stopped"}[o.Intent]
	if o.Mode == "play_now" {
		what = "Playing a selection"
	}
	reason := what + " from " + o.Source
	end, hasEnd := overrideEnd(o, ivs)
	if hasEnd {
		reason += ", schedule resumes " + fmtWhen(end, now, loc)
	} else {
		reason += " until Resume schedule"
	}
	out := player.Owner{State: player.OwnerOverridden, Reason: reason}
	if hasEnd {
		out.Until = &end
	}
	return out
}

// fmtWhen writes a time as HH:MM today, or with the weekday further out.
func fmtWhen(t, now time.Time, loc *time.Location) string {
	lt := t.In(loc)
	if lt.YearDay() == now.In(loc).YearDay() && lt.Year() == now.In(loc).Year() {
		return lt.Format("15:04")
	}
	return lt.Format("Mon 15:04")
}

// overrideEnd returns when an override ends by the schedule: the next
// boundary after it was created, and for a timed one the earlier of that
// and its own end.
func overrideEnd(o store.Override, ivs []Interval) (time.Time, bool) {
	next, ok := NextBoundary(ivs, o.CreatedAt)
	if o.Mode == "timed" && o.EndsAt != nil && (!ok || o.EndsAt.Before(next)) {
		return *o.EndsAt, true
	}
	return next, ok
}

// activeOverride returns the override when it has not ended.
func (s *Scheduler) activeOverride(ctx context.Context, now time.Time, ivs []Interval) (store.Override, bool) {
	o, ok, err := s.d.Store.GetOverride(ctx)
	if err != nil || !ok {
		return o, false
	}
	if end, has := overrideEnd(o, ivs); has && !now.Before(end) {
		return o, false
	}
	return o, true
}

// CreateOverride stores an override and reconciles.
func (s *Scheduler) CreateOverride(ctx context.Context, o store.Override) error {
	o.CreatedAt = s.d.Clock.Now()
	o.Generation = s.d.Player.Generation()
	if err := s.d.Store.SetOverride(ctx, o); err != nil {
		return err
	}
	s.d.Events.Publish(events.Schedule, "")
	s.Kick()
	return nil
}

// CreateOverrideKeepingEnd stores a changed override without a new
// creation time, so a timed override keeps its end.
func (s *Scheduler) CreateOverrideKeepingEnd(ctx context.Context, o store.Override) error {
	if err := s.d.Store.SetOverride(ctx, o); err != nil {
		return err
	}
	s.d.Events.Publish(events.Schedule, "")
	s.Kick()
	return nil
}

// ClearOverride ends the override and lets the schedule take over.
func (s *Scheduler) ClearOverride(ctx context.Context) error {
	if err := s.d.Store.ClearOverride(ctx); err != nil {
		return err
	}
	s.d.Events.Publish(events.Schedule, "")
	s.Kick()
	return nil
}

// Override returns the active override for the API.
func (s *Scheduler) Override(ctx context.Context) (store.Override, *time.Time, bool) {
	now := s.d.Clock.Now()
	ivs := s.current(ctx, now)
	o, ok := s.activeOverride(ctx, now, ivs)
	if !ok {
		return o, nil, false
	}
	if end, has := overrideEnd(o, ivs); has {
		return o, &end, true
	}
	return o, nil, true
}

// Loaded returns the loaded program.
func (s *Scheduler) Loaded() Program {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded
}

// Tick runs one reconciliation.
func (s *Scheduler) Tick(ctx context.Context) {
	owner := s.Owner(ctx)
	s.mu.Lock()
	changed := owner.State != s.lastOwner.State || owner.Reason != s.lastOwner.Reason
	s.lastOwner = owner
	fade := s.fade
	s.mu.Unlock()
	if changed {
		s.d.Events.Publish(events.Player, "")
	}
	if owner.State == player.OwnerManual || owner.State == player.OwnerUnavailable {
		if fade != nil {
			s.cancelFade()
		}
		return
	}
	st, err := s.d.Player.Status()
	if err != nil {
		return
	}
	now := s.d.Clock.Now()
	ivs := s.current(ctx, now)
	want, hasWant := Current(ivs, now)

	// A fade is a state the loop knows about. While one runs, the only
	// question is whether its reason still holds.
	if fade != nil {
		hold := false
		switch fade.kind {
		case "out":
			hold = owner.State == player.OwnerScheduled && (!hasWant || want.Key != fade.key)
		case "in":
			hold = owner.State == player.OwnerScheduled && hasWant && want.Key == fade.key
		}
		if !hold {
			s.cancelFade()
		}
		return
	}

	if owner.State == player.OwnerOverridden {
		s.enforceOverride(ctx, st)
		return
	}

	loaded := s.Loaded()
	switch {
	case !hasWant:
		if st.State == "play" {
			s.startFade(ctx, "out", loaded.Key, s.fadeOutStop)
		}
	case want.Key != loaded.Key:
		same := want.Source == loaded.Source && want.Options.Shuffle == loaded.Options.Shuffle &&
			st.State == "play" && loaded.Generation == s.d.Player.Generation()
		if same {
			// Back-to-back windows with the same program keep playing.
			s.applyVolume(want.Options)
			s.setLoaded(ctx, Program{Key: want.Key, Name: want.Name, Source: want.Source, Options: want.Options, Generation: loaded.Generation})
			return
		}
		if st.State == "play" {
			s.startFade(ctx, "out", want.Key, func(fctx context.Context) {
				s.fadeOutStop(fctx)
				if fctx.Err() == nil {
					s.loadAndPlay(fctx, want)
				}
			})
			return
		}
		s.loadAndPlay(ctx, want)
	case st.State != "play":
		if err := s.d.Player.Play(); err != nil {
			s.notePlayFailure(err.Error())
			return
		}
		s.resetFailures()
	default:
		s.resetFailures()
	}
}

// enforceOverride keeps MPD in the state the person asked for.
func (s *Scheduler) enforceOverride(ctx context.Context, st player.Status) {
	o, ok, err := s.d.Store.GetOverride(ctx)
	if err != nil || !ok {
		return
	}
	switch o.Intent {
	case "play":
		if o.Mode == "play_now" && s.d.Player.Finished(st) {
			// The chosen tracks finished: the override is over.
			s.ClearOverride(ctx)
			return
		}
		if st.State != "play" && o.Generation == s.d.Player.Generation() && !s.d.Player.Finished(st) {
			if err := s.d.Player.Play(); err != nil {
				s.notePlayFailure(err.Error())
			}
		}
	case "pause":
		if st.State == "play" {
			s.d.Player.Pause()
		}
	case "stop":
		if st.State == "play" {
			s.d.Player.Stop()
		}
	}
}

// loadAndPlay resolves and loads a program, applies its volume and starts
// it with a fade-in.
func (s *Scheduler) loadAndPlay(ctx context.Context, want Interval) {
	files, truncated, err := s.d.Resolve(ctx, want.Source)
	if err != nil || len(files) == 0 {
		msg := fmt.Sprintf("%s has nothing to play", want.Name)
		if err != nil {
			msg = fmt.Sprintf("%s cannot be loaded: %v", want.Name, err)
		}
		s.notePlayFailure(msg)
		return
	}
	if truncated {
		s.d.Log.Warn("program truncated to the queue ceiling", "program", want.Name)
	}
	set := s.d.Settings()
	volume := -1
	if want.Options.Volume != nil {
		volume = clamp(*want.Options.Volume, set.VolumeMin, set.VolumeMax)
	}
	fadeIn := set.FadeIn > 0
	if fadeIn {
		// The fade sets the target itself.
		s.d.Player.SetVolumeRaw(0)
	}
	if _, err := s.d.Player.Load(files, want.Options.Shuffle, volume); err != nil {
		s.notePlayFailure(fmt.Sprintf("%s cannot start: %v", want.Name, err))
		return
	}
	s.resetFailures()
	s.setLoaded(ctx, Program{Key: want.Key, Name: want.Name, Source: want.Source, Options: want.Options, Generation: s.d.Player.Generation()})
	s.d.Log.Info("program started", "program", want.Name, "tracks", len(files))
	s.d.OnProgram(want.Name)
	if fadeIn {
		target := volume
		if target < 0 {
			target = s.lastVolume()
		}
		s.startFade(ctx, "in", want.Key, func(fctx context.Context) { s.fadeTo(fctx, 0, target, set.FadeIn) })
	}
	s.d.Events.Publish(events.Player, "")
}

// lastVolume is the volume MPD had before a fade-in set it to zero.
func (s *Scheduler) lastVolume() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restoreVolume > 0 {
		return s.restoreVolume
	}
	return 50
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

func (s *Scheduler) applyVolume(o Options) {
	if o.Volume == nil {
		return
	}
	set := s.d.Settings()
	s.d.Player.SetVolumeRaw(clamp(*o.Volume, set.VolumeMin, set.VolumeMax))
}

func (s *Scheduler) setLoaded(ctx context.Context, p Program) {
	s.mu.Lock()
	s.loaded = p
	s.mu.Unlock()
	if err := s.d.Store.SetState(ctx, programKey, p); err != nil {
		s.d.Log.Warn("cannot persist the loaded program", "error", err)
	}
}

// ForgetProgram clears the loaded program, after the queue was replaced by
// something that is not a schedule program.
func (s *Scheduler) ForgetProgram(ctx context.Context) {
	s.setLoaded(ctx, Program{})
}

func (s *Scheduler) notePlayFailure(msg string) {
	s.mu.Lock()
	s.failures++
	n := s.failures
	s.mu.Unlock()
	s.d.Log.Warn("cannot keep the schedule playing", "reason", msg, "failures", n)
	if n == 3 {
		s.d.OnProblem("dead_air", msg)
	}
}

func (s *Scheduler) resetFailures() {
	s.mu.Lock()
	if s.failures >= 3 {
		s.d.OnProblem("dead_air_cleared", "")
	}
	s.failures = 0
	s.mu.Unlock()
}

// startFade runs fn in a goroutine with a cancellable context and records
// it as the fade in progress. fn must return when its context ends.
func (s *Scheduler) startFade(ctx context.Context, kind, key string, fn func(context.Context)) {
	fctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f := &fadeState{kind: kind, key: key, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.fade = f
	s.mu.Unlock()
	go func() {
		defer close(f.done)
		fn(fctx)
		s.mu.Lock()
		if s.fade == f {
			s.fade = nil
		}
		s.mu.Unlock()
		cancel()
		s.Kick()
	}()
}

// cancelFade stops the fade in progress and waits for it.
func (s *Scheduler) cancelFade() {
	s.mu.Lock()
	f := s.fade
	s.fade = nil
	s.mu.Unlock()
	if f == nil {
		return
	}
	f.cancel()
	<-f.done
}

// fadeOutStop steps the volume down over the fade-out period, stops, and
// restores the volume so the next start is not silent. Both fades bypass
// the configured floor.
func (s *Scheduler) fadeOutStop(ctx context.Context) {
	set := s.d.Settings()
	st, err := s.d.Player.Status()
	if err != nil {
		return
	}
	vol := st.Volume
	if vol < 0 {
		vol = 50
	}
	s.mu.Lock()
	s.restoreVolume = vol
	s.mu.Unlock()
	if set.FadeOut > 0 && vol > 0 {
		s.fadeTo(ctx, vol, 0, set.FadeOut)
	}
	if ctx.Err() != nil {
		s.d.Player.SetVolumeRaw(vol)
		return
	}
	s.d.Player.Stop()
	s.d.Player.SetVolumeRaw(vol)
	s.d.Events.Publish(events.Player, "")
}

// fadeTo steps the volume from one level to another over seconds, in
// twenty steps, and stops early when ctx ends.
func (s *Scheduler) fadeTo(ctx context.Context, from, to, seconds int) {
	const steps = 20
	if seconds <= 0 {
		s.d.Player.SetVolumeRaw(to)
		return
	}
	pause := time.Duration(seconds) * time.Second / steps
	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		v := from + (to-from)*i/steps
		if err := s.d.Player.SetVolumeRaw(v); err != nil {
			return
		}
	}
}

// ErrSchedulerOff reports an action that needs the scheduler on.
var ErrSchedulerOff = errors.New("the scheduler is off")
