package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"jukem/internal/events"
	"jukem/internal/player"
	"jukem/internal/store"
)

// Player is what the reconciler needs from the player.
type Player interface {
	Status() (player.Status, error)
	Load(files []string, shuffle bool, volume int, repeat bool) (int, error)
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
	d         Deps
	kick      chan struct{}
	suspended atomic.Int32

	// tickMu is held while a tick runs, so Suspend can wait for it.
	tickMu sync.Mutex

	mu     sync.Mutex
	ivs    []Interval
	ivsAt  time.Time // zero when the cache is stale
	loaded Program
	fade   *fadeState
	// failures counts play attempts that did not result in playback, and
	// retryAfter spaces the attempts out.
	failures   int
	retryAfter time.Time
	// restoreVolume is the level before a fade-out, for the next start.
	restoreVolume int
	// lastOwner avoids an event on every tick.
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
	s := &Scheduler{d: d, kick: make(chan struct{}, 1)}
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

// Suspend holds the loop while a person's action runs, so a tick cannot
// undo it before its override exists. A fade in progress is stopped too,
// because its goroutine would stop or replace the queue. The returned
// function releases the hold and reconciles.
func (s *Scheduler) Suspend() func() {
	s.suspended.Add(1)
	s.cancelFade(nil)
	// A tick that is already running holds tickMu. Waiting for it here
	// means the loop cannot act after Suspend returns.
	s.tickMu.Lock()
	s.tickMu.Unlock() //nolint:staticcheck // the lock is a handshake, not a guard
	return func() {
		s.suspended.Add(-1)
		s.Kick()
	}
}

// Invalidate drops the interval cache after a rule, exception, playlist
// or zone change, tells the clients, and reconciles.
func (s *Scheduler) Invalidate() {
	s.mu.Lock()
	s.ivsAt = time.Time{}
	s.mu.Unlock()
	s.d.Events.Publish(events.Schedule, "")
	s.Kick()
}

// Run reconciles every tickInterval and after every kick until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.cancelFade(nil)
			return
		case <-tick.C:
		case <-s.kick:
		}
		s.Tick(ctx)
	}
}

// Intervals returns the expanded intervals for a range.
func (s *Scheduler) Intervals(ctx context.Context, from, to time.Time) ([]Interval, error) {
	set := s.d.Settings()
	loc := set.Location()
	rules, err := s.d.Store.ListSchedules(ctx)
	if err != nil {
		return nil, err
	}
	excs, err := s.d.Store.ListExceptions(ctx, from.In(loc).AddDate(0, 0, -1).Format("2006-01-02"), to.In(loc).AddDate(0, 0, 1).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	return Expand(rules, excs, loc, from, to), nil
}

// current returns the cached rolling window, refreshed when stale or once
// an hour, so that a new day rolls in.
func (s *Scheduler) current(ctx context.Context, now time.Time) []Interval {
	s.mu.Lock()
	if !s.ivsAt.IsZero() && now.Sub(s.ivsAt) < time.Hour {
		ivs := s.ivs
		s.mu.Unlock()
		return ivs
	}
	s.mu.Unlock()
	ivs, err := s.Intervals(ctx, now.AddDate(0, 0, -1), now.AddDate(0, 0, 8))
	if err != nil {
		// The last good window is better than an empty one, which would
		// stop the music.
		s.d.Log.Warn("cannot expand the schedule", "error", err)
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.ivs
	}
	s.mu.Lock()
	s.ivs, s.ivsAt = ivs, now
	s.mu.Unlock()
	return ivs
}

// view is everything one decision needs, computed once.
type view struct {
	now      time.Time
	loc      *time.Location
	set      store.Settings
	ivs      []Interval
	owner    player.Owner
	override store.Override
	hasOver  bool
	overEnd  time.Time
	hasEnd   bool
	expired  bool // an override past its end is stored; Tick removes it
}

// snapshot computes the owner and the facts behind it.
func (s *Scheduler) snapshot(ctx context.Context) view {
	v := view{now: s.d.Clock.Now(), set: s.d.Settings()}
	v.loc = v.set.Location()
	v.ivs = s.current(ctx, v.now)
	// The override is read first, so a caller sees it while MPD or the
	// device is away. An override past its end is removed at once: its
	// end is computed from the rolling window, and a stale row would come
	// back once its window left the cache.
	if o, ok, err := s.d.Store.GetOverride(ctx); err == nil && ok {
		end, hasEnd := overrideEnd(o, v.ivs)
		if !hasEnd || v.now.Before(end) {
			v.override, v.hasOver, v.overEnd, v.hasEnd = o, true, end, hasEnd
		} else {
			v.expired = true
		}
	}
	if !s.d.MPDRunning() {
		v.owner = player.Owner{State: player.OwnerUnavailable, Reason: "MPD is not running"}
		return v
	}
	warning := ""
	if !s.d.DevicePresent() {
		warning = "Output device missing"
	} else if !s.d.Clock.Trusted(ctx) {
		warning = "Clock not set"
	}
	if !v.set.SchedulerEnabled {
		v.owner = player.Owner{State: player.OwnerManual, Reason: "Scheduler off", Warning: warning}
		return v
	}
	if warning != "" {
		v.owner = player.Owner{State: player.OwnerUnavailable, Reason: warning}
		return v
	}
	if v.hasOver {
		v.owner = overrideOwner(v.override, v.now, v.overEnd, v.hasEnd, v.loc, v.set.ClockLayout())
		// The rule that plays when the schedule takes over again is the one
		// of that moment, not the one of now: a stop usually ends where the
		// window of now ends.
		if v.hasEnd {
			if iv, ok := Current(v.ivs, v.overEnd); ok {
				v.owner.Program = iv.Name
			}
		} else if iv, ok := Current(v.ivs, v.now); ok {
			v.owner.Program = iv.Name
		}
		return v
	}
	if iv, ok := Current(v.ivs, v.now); ok {
		v.owner = player.Owner{State: player.OwnerScheduled, Reason: fmt.Sprintf("%s until %s", iv.Name, fmtWhen(iv.End, v.now, v.loc, v.set.ClockLayout())), Program: iv.Name, Since: &iv.Start, Until: &iv.End}
		return v
	}
	for _, iv := range v.ivs {
		if iv.Start.After(v.now) {
			v.owner = player.Owner{State: player.OwnerScheduled, Reason: "Nothing scheduled until " + fmtWhen(iv.Start, v.now, v.loc, v.set.ClockLayout()), Until: &iv.Start}
			return v
		}
	}
	v.owner = player.Owner{State: player.OwnerScheduled, Reason: "Nothing scheduled in the next week"}
	return v
}

// Owner reports who decides playback now, with the reason in plain words.
func (s *Scheduler) Owner(ctx context.Context) player.Owner {
	return s.snapshot(ctx).owner
}

func overrideOwner(o store.Override, now, end time.Time, hasEnd bool, loc *time.Location, layout string) player.Owner {
	what := map[string]string{"play": "Playing", "pause": "Paused", "stop": "Stopped"}[o.Intent]
	if o.Mode == "play_now" {
		what = "Playing a selection"
	}
	reason := what + " from " + o.Source
	if hasEnd {
		reason += ", schedule resumes " + fmtWhen(end, now, loc, layout)
	} else {
		reason += " until you start the scheduler"
	}
	out := player.Owner{State: player.OwnerOverridden, Reason: reason}
	if hasEnd {
		out.Until = &end
	}
	return out
}

// fmtWhen writes a time of day for today, or with the weekday further
// out. layout is the clock layout of the settings.
func fmtWhen(t, now time.Time, loc *time.Location, layout string) string {
	lt, nl := t.In(loc), now.In(loc)
	if lt.Format("2006-01-02") == nl.Format("2006-01-02") {
		return lt.Format(layout)
	}
	return lt.Format("Mon " + layout)
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

// CreateOverride stores an override and reconciles.
func (s *Scheduler) CreateOverride(ctx context.Context, o store.Override) error {
	o.CreatedAt = s.d.Clock.Now()
	o.Generation = s.d.Player.Generation()
	return s.storeOverride(ctx, o)
}

// UpdateOverrideIntent changes what an active override asks for and keeps
// its end.
func (s *Scheduler) UpdateOverrideIntent(ctx context.Context, o store.Override, intent string) error {
	o.Intent = intent
	o.Generation = s.d.Player.Generation()
	return s.storeOverride(ctx, o)
}

func (s *Scheduler) storeOverride(ctx context.Context, o store.Override) error {
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

// Override returns the active override for the API, with its end when
// the schedule gives one.
func (s *Scheduler) Override(ctx context.Context) (store.Override, *time.Time, bool) {
	v := s.snapshot(ctx)
	if !v.hasOver {
		return store.Override{}, nil, false
	}
	if v.hasEnd {
		end := v.overEnd
		return v.override, &end, true
	}
	return v.override, nil, true
}

// PlaySource names who put the music on, for the history: the program,
// the person behind an override, or "manual".
func (s *Scheduler) PlaySource(ctx context.Context) string {
	v := s.snapshot(ctx)
	switch {
	case v.hasOver:
		return v.override.Source
	case v.owner.State == player.OwnerScheduled && v.owner.Program != "":
		return v.owner.Program
	}
	return "manual"
}

// Fading reports whether a fade runs. The MPD watcher then skips the
// volume events, which would otherwise make every client refetch the
// status twenty times.
func (s *Scheduler) Fading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fade != nil
}

// Loaded returns the loaded program.
func (s *Scheduler) Loaded() Program {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded
}

// Tick runs one reconciliation.
func (s *Scheduler) Tick(ctx context.Context) {
	// The lock is taken first, so a Suspend that waits for this tick knows
	// the loop is done when it returns.
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	if s.suspended.Load() > 0 {
		return
	}
	v := s.snapshot(ctx)
	if v.expired {
		s.ClearOverride(ctx)
	}
	s.mu.Lock()
	changed := v.owner.State != s.lastOwner.State || v.owner.Reason != s.lastOwner.Reason
	s.lastOwner = v.owner
	fade := s.fade
	s.mu.Unlock()
	if changed {
		s.d.Events.Publish(events.Player, "")
	}
	if v.owner.State == player.OwnerManual || v.owner.State == player.OwnerUnavailable {
		s.cancelFade(fade)
		return
	}
	st, err := s.d.Player.Status()
	if err != nil {
		return
	}
	want, hasWant := Current(v.ivs, v.now)

	// A fade is a state the loop knows about. While one runs, the only
	// question is whether its reason still holds.
	if fade != nil {
		hold := false
		switch fade.kind {
		case "out":
			hold = v.owner.State == player.OwnerScheduled && (!hasWant || want.Key != fade.key)
		case "in":
			hold = v.owner.State == player.OwnerScheduled && hasWant && want.Key == fade.key
		}
		if !hold {
			s.cancelFade(fade)
		}
		return
	}

	if v.owner.State == player.OwnerOverridden {
		s.enforceOverride(ctx, v.override, st)
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
			// The fade belongs to the program that ends.
			s.startFade(ctx, "out", loaded.Key, func(fctx context.Context) {
				s.fadeOutStop(fctx)
				if fctx.Err() == nil {
					s.loadAndPlay(fctx, want)
				}
			})
			return
		}
		s.loadAndPlay(ctx, want)
	case st.State != "play":
		s.tryPlay(v.now, st)
	default:
		s.resetFailures()
	}
}

// tryPlay presses play when the schedule says so, spaced out after
// failures. A stop with an MPD error counts as a failure too, because
// MPD accepts play and then stops when the output cannot open.
func (s *Scheduler) tryPlay(now time.Time, st player.Status) {
	s.mu.Lock()
	wait := now.Before(s.retryAfter)
	s.mu.Unlock()
	if wait {
		return
	}
	if st.Error != "" {
		s.notePlayFailure(now, "MPD reports: "+st.Error)
	}
	if err := s.d.Player.Play(); err != nil {
		s.notePlayFailure(now, err.Error())
	}
}

// enforceOverride keeps MPD in the state the person asked for.
func (s *Scheduler) enforceOverride(ctx context.Context, o store.Override, st player.Status) {
	switch o.Intent {
	case "play":
		if o.Mode == "play_now" && (s.d.Player.Finished(st) || (st.State == "stop" && st.Error != "")) {
			// The chosen tracks finished, or the last one failed: the
			// override is over either way.
			s.ClearOverride(ctx)
			return
		}
		if st.State != "play" && o.Generation == s.d.Player.Generation() && !s.d.Player.Finished(st) {
			s.tryPlay(s.d.Clock.Now(), st)
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
// it, with a fade-in when one is configured.
func (s *Scheduler) loadAndPlay(ctx context.Context, want Interval) {
	now := s.d.Clock.Now()
	files, truncated, err := s.d.Resolve(ctx, want.Source)
	if ctx.Err() != nil {
		// A cancelled fade is not a play failure.
		return
	}
	if err != nil || len(files) == 0 {
		msg := fmt.Sprintf("%s has nothing to play", want.Name)
		if err != nil {
			msg = fmt.Sprintf("%s cannot be loaded: %v", want.Name, err)
		}
		s.notePlayFailure(now, msg)
		return
	}
	if truncated {
		s.d.Log.Warn("program truncated to the queue ceiling", "program", want.Name)
	}
	set := s.d.Settings()
	target := s.currentVolume()
	if want.Options.Volume != nil {
		target = clamp(*want.Options.Volume, set.VolumeMin, set.VolumeMax)
	}
	fadeIn := set.FadeIn > 0
	volume := target
	if fadeIn {
		// The fade sets the level itself, from silence.
		volume = 0
	}
	if _, err := s.d.Player.Load(files, want.Options.Shuffle, volume, true); err != nil {
		s.notePlayFailure(now, fmt.Sprintf("%s cannot start: %v", want.Name, err))
		return
	}
	s.resetFailures()
	s.setLoaded(ctx, Program{Key: want.Key, Name: want.Name, Source: want.Source, Options: want.Options, Generation: s.d.Player.Generation()})
	s.d.Log.Info("program started", "program", want.Name, "tracks", len(files))
	if fadeIn {
		s.startFade(ctx, "in", want.Key, func(fctx context.Context) {
			s.fadeTo(fctx, 0, target, set.FadeIn)
			// A cancelled fade-in must not leave the music quiet.
			s.d.Player.SetVolumeRaw(target)
		})
	}
	s.d.Events.Publish(events.Player, "")
}

// currentVolume is the level to keep: MPD's current volume, or the level
// before the last fade-out.
func (s *Scheduler) currentVolume() int {
	if st, err := s.d.Player.Status(); err == nil && st.Volume >= 0 {
		return st.Volume
	}
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

// notePlayFailure counts a failure, spaces out the next attempt, and
// reports dead air after three.
func (s *Scheduler) notePlayFailure(now time.Time, msg string) {
	s.mu.Lock()
	s.failures++
	n := s.failures
	s.retryAfter = now.Add(min(time.Duration(n)*tickInterval, time.Minute))
	s.mu.Unlock()
	s.d.Log.Warn("cannot keep the schedule playing", "reason", msg, "failures", n)
	if n == 3 {
		s.d.OnProblem("dead_air", msg)
	}
}

func (s *Scheduler) resetFailures() {
	s.mu.Lock()
	cleared := s.failures >= 3
	s.failures = 0
	s.retryAfter = time.Time{}
	s.mu.Unlock()
	if cleared {
		s.d.OnProblem("dead_air_cleared", "")
	}
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

// cancelFade stops the given fade and waits for it. With nil it stops
// whatever fade runs. A fade that already ended, or was replaced by a
// newer one, is left alone.
func (s *Scheduler) cancelFade(f *fadeState) {
	s.mu.Lock()
	if f == nil {
		f = s.fade
	}
	if f == nil || s.fade != f {
		s.mu.Unlock()
		return
	}
	s.fade = nil
	s.mu.Unlock()
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

// fadeTo steps the volume from one level to another over seconds.
func (s *Scheduler) fadeTo(ctx context.Context, from, to, seconds int) {
	Fade(ctx, s.d.Player.SetVolumeRaw, from, to, seconds)
}

// Fade steps the volume from one level to another over seconds, in twenty
// steps. It stops early when ctx ends or when a step fails. Seconds of zero
// sets the level at once. The announcements fade with it too, so a fade
// sounds the same wherever it comes from.
func Fade(ctx context.Context, setVolume func(int) error, from, to, seconds int) {
	const steps = 20
	if seconds <= 0 || from == to {
		setVolume(to)
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
		if err := setVolume(v); err != nil {
			return
		}
	}
}
