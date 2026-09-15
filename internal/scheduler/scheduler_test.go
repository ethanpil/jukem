package scheduler

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"jukem/internal/events"
	"jukem/internal/player"
	"jukem/internal/store"
)

// fakePlayer records what the reconciler asks for.
type fakePlayer struct {
	mu       sync.Mutex
	state    string
	volume   int
	loaded   [][]string
	gen      int64
	plays    int
	stops    int
	pauses   int
	finished bool
	playErr  error
}

func (f *fakePlayer) Status() (player.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return player.Status{State: f.state, Volume: f.volume, QueueLength: 3}, nil
}
func (f *fakePlayer) Load(files []string, shuffle bool, volume int, repeat bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loaded = append(f.loaded, files)
	f.gen++
	f.state = "play"
	if volume >= 0 {
		f.volume = volume
	}
	return len(files), nil
}
func (f *fakePlayer) Play() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.playErr != nil {
		return f.playErr
	}
	f.plays++
	f.state = "play"
	return nil
}
func (f *fakePlayer) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.state = "stop"
	return nil
}
func (f *fakePlayer) Pause() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauses++
	f.state = "pause"
	return nil
}
func (f *fakePlayer) SetVolumeRaw(v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volume = v
	return nil
}
func (f *fakePlayer) Finished(player.Status) bool { return f.finished }
func (f *fakePlayer) Generation() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen
}

// playerState is a copy of the fake's counters without its lock.
type playerState struct {
	state  string
	volume int
	loaded [][]string
	gen    int64
	plays  int
	stops  int
	pauses int
}

func (f *fakePlayer) snapshot() playerState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return playerState{state: f.state, volume: f.volume, loaded: f.loaded, gen: f.gen, plays: f.plays, stops: f.stops, pauses: f.pauses}
}

type harness struct {
	s     *Scheduler
	p     *fakePlayer
	st    *store.Store
	now   time.Time
	loc   *time.Location
	clock *Clock
	set   store.Settings
	mu    sync.Mutex
}

func newHarness(t *testing.T) *harness {
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
	loc := mustLoc(t, "Europe/London")
	h := &harness{p: &fakePlayer{state: "stop", volume: 60}, st: st, loc: loc}
	h.now = time.Date(2026, 1, 5, 8, 0, 0, 0, loc) // Monday 08:00
	h.set = store.DefaultSettings()
	h.set.TimeZone = "Europe/London"
	h.set.FadeOut = 0
	h.set.FadeIn = 0
	h.clock = &Clock{store: st, buildTime: time.Time{}, probe: func() (bool, bool) { return true, true }, bootID: func() string { return "boot" }}
	h.clock.manual = &manualOffset{OffsetSeconds: h.now.Sub(time.Now()).Seconds(), BootID: "boot", SetAt: time.Now()}
	h.clock.probe = func() (bool, bool) { return false, true } // keep the manual offset
	h.s = New(context.Background(), Deps{
		Store: st, Player: h.p, Events: events.New(), Clock: h.clock, Log: slog.New(slog.DiscardHandler),
		Settings: func() store.Settings { h.mu.Lock(); defer h.mu.Unlock(); return h.set },
		Resolve: func(ctx context.Context, src Source) ([]string, bool, error) {
			return []string{src.Ref + "/a.mp3", src.Ref + "/b.mp3"}, false, nil
		},
		MPDRunning:    func() bool { return true },
		DevicePresent: func() bool { return true },
	})
	return h
}

// advance moves the scheduler's clock.
func (h *harness) advance(d time.Duration) {
	h.now = h.now.Add(d)
	h.clock.mu.Lock()
	h.clock.manual.OffsetSeconds = h.now.Sub(time.Now()).Seconds()
	h.clock.mu.Unlock()
}

func (h *harness) tick() { h.s.Tick(context.Background()) }

// waitFade waits for a fade in progress to finish.
func (h *harness) waitFade(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.s.mu.Lock()
		f := h.s.fade
		h.s.mu.Unlock()
		if f == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("fade did not finish")
}

func (h *harness) addRule(t *testing.T, name string, start, end string, ref string) int64 {
	t.Helper()
	id, err := h.st.CreateSchedule(context.Background(), store.Schedule{Name: name, Enabled: true, Days: store.AllDays, StartTime: start, EndTime: end, SourceType: "directory", SourceRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	h.s.Invalidate()
	return id
}

func TestWindowStartsAndEnds(t *testing.T) {
	h := newHarness(t)
	h.addRule(t, "Morning", "09:00", "11:00", "Morning")
	h.tick()
	if len(h.p.snapshot().loaded) != 0 {
		t.Fatal("loaded before the window")
	}
	if o := h.s.Owner(context.Background()); o.State != player.OwnerScheduled || o.Reason != "Nothing scheduled until 09:00" {
		t.Fatalf("owner %+v", o)
	}
	h.advance(90 * time.Minute) // 09:30
	h.tick()
	p := h.p.snapshot()
	if len(p.loaded) != 1 || p.loaded[0][0] != "Morning/a.mp3" || p.state != "play" {
		t.Fatalf("not loaded: %+v", p)
	}
	if o := h.s.Owner(context.Background()); o.Reason != "Morning until 11:00" {
		t.Fatalf("owner %+v", o)
	}
	// A second tick inside the window changes nothing.
	h.tick()
	if len(h.p.snapshot().loaded) != 1 {
		t.Fatal("reloaded inside the window")
	}
	// MPD stopped by itself: the loop presses play.
	h.p.mu.Lock()
	h.p.state = "stop"
	h.p.mu.Unlock()
	h.tick()
	if p := h.p.snapshot(); p.state != "play" || p.plays != 1 {
		t.Fatalf("not resumed: %+v", p)
	}
	// The window ends: stop.
	h.advance(2 * time.Hour) // 11:30
	h.tick()
	h.waitFade(t)
	if p := h.p.snapshot(); p.state != "stop" || p.stops != 1 {
		t.Fatalf("not stopped after the window: %+v", p)
	}
}

func TestBackToBackSameProgramKeepsPlaying(t *testing.T) {
	h := newHarness(t)
	h.addRule(t, "First", "09:00", "10:00", "Same")
	h.addRule(t, "Second", "10:00", "11:00", "Same")
	h.advance(90 * time.Minute)
	h.tick()
	h.advance(time.Hour) // 10:30
	h.tick()
	if p := h.p.snapshot(); len(p.loaded) != 1 || p.stops != 0 {
		t.Fatalf("restarted the same program: %+v", p)
	}
	if h.s.Loaded().Name != "Second" {
		t.Fatalf("key not adopted: %+v", h.s.Loaded())
	}
}

func TestDifferentProgramReloads(t *testing.T) {
	h := newHarness(t)
	h.addRule(t, "First", "09:00", "10:00", "One")
	h.addRule(t, "Second", "10:00", "11:00", "Two")
	h.advance(90 * time.Minute)
	h.tick()
	h.advance(time.Hour)
	h.tick()
	h.waitFade(t)
	// The fade-out goroutine loads the next program; wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(h.p.snapshot().loaded) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if p := h.p.snapshot(); len(p.loaded) != 2 || p.loaded[1][0] != "Two/a.mp3" {
		t.Fatalf("second program not loaded: %+v", p)
	}
}

func TestOverrideEndsAtNextEvent(t *testing.T) {
	h := newHarness(t)
	h.addRule(t, "Morning", "09:00", "11:00", "Morning")
	h.advance(90 * time.Minute)
	h.tick()
	ctx := context.Background()
	if err := h.s.CreateOverride(ctx, store.Override{Mode: "until_next", Intent: "pause", Source: "web UI"}); err != nil {
		t.Fatal(err)
	}
	h.tick()
	if o := h.s.Owner(ctx); o.State != player.OwnerOverridden || o.Reason != "Paused from web UI, schedule resumes 11:00" {
		t.Fatalf("owner %+v", o)
	}
	if p := h.p.snapshot(); p.state != "pause" {
		t.Fatalf("override not enforced: %+v", p)
	}
	// The schedule does not undo the pause inside the window.
	h.advance(10 * time.Minute)
	h.tick()
	if p := h.p.snapshot(); p.state != "pause" || p.plays != 0 {
		t.Fatalf("schedule overrode the person: %+v", p)
	}
	// At the next event the override is over and the loop stops the music.
	h.advance(80 * time.Minute) // 11:10
	h.tick()
	if o := h.s.Owner(ctx); o.State != player.OwnerOverridden {
		// Good: override ended.
	} else {
		t.Fatalf("override outlived the next event: %+v", o)
	}
	if _, _, ok := h.s.Override(ctx); ok {
		t.Fatal("override still active")
	}
}

func TestTimedOverrideEndsEarlyAtNextEvent(t *testing.T) {
	h := newHarness(t)
	h.addRule(t, "Morning", "09:00", "11:00", "Morning")
	h.advance(90 * time.Minute)
	h.tick()
	ctx := context.Background()
	end := h.now.Add(5 * time.Hour)
	h.s.CreateOverride(ctx, store.Override{Mode: "timed", Intent: "play", EndsAt: &end, Source: "web UI"})
	_, endsAt, ok := h.s.Override(ctx)
	if !ok || endsAt == nil || endsAt.In(h.loc).Format("15:04") != "11:00" {
		t.Fatalf("timed override should end at the next event: %v", endsAt)
	}
	// A short timed override ends at its own end.
	end = h.now.Add(15 * time.Minute)
	h.s.CreateOverride(ctx, store.Override{Mode: "timed", Intent: "play", EndsAt: &end, Source: "web UI"})
	_, endsAt, _ = h.s.Override(ctx)
	if endsAt.In(h.loc).Format("15:04") != "09:45" {
		t.Fatalf("got %v", endsAt)
	}
}

func TestPlayNowOverrideEndsWhenFinished(t *testing.T) {
	h := newHarness(t)
	h.addRule(t, "Morning", "09:00", "11:00", "Morning")
	h.advance(90 * time.Minute)
	h.tick()
	ctx := context.Background()
	h.s.ForgetProgram(ctx)
	h.s.CreateOverride(ctx, store.Override{Mode: "play_now", Intent: "play", Source: "web UI"})
	h.tick()
	if _, _, ok := h.s.Override(ctx); !ok {
		t.Fatal("override missing")
	}
	// The selection finishes: the override ends and the schedule reloads.
	h.p.mu.Lock()
	h.p.state = "stop"
	h.p.finished = true
	h.p.mu.Unlock()
	h.tick()
	if _, _, ok := h.s.Override(ctx); ok {
		t.Fatal("override survived the end of the selection")
	}
	h.p.finished = false
	h.tick()
	if p := h.p.snapshot(); len(p.loaded) != 2 {
		t.Fatalf("schedule not reloaded after Play Now: %+v", p)
	}
}

func TestManualModeDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.addRule(t, "Morning", "09:00", "11:00", "Morning")
	h.mu.Lock()
	h.set.SchedulerEnabled = false
	h.mu.Unlock()
	h.advance(90 * time.Minute)
	h.tick()
	if p := h.p.snapshot(); len(p.loaded) != 0 || p.plays != 0 {
		t.Fatalf("manual mode acted: %+v", p)
	}
	if o := h.s.Owner(context.Background()); o.State != player.OwnerManual {
		t.Fatalf("owner %+v", o)
	}
}

func TestDeadAirReported(t *testing.T) {
	h := newHarness(t)
	var problems []string
	h.s.d.OnProblem = func(kind, msg string) { problems = append(problems, kind) }
	h.addRule(t, "Morning", "09:00", "11:00", "Morning")
	h.advance(90 * time.Minute)
	h.tick()
	h.p.mu.Lock()
	h.p.state = "stop"
	h.p.playErr = context.DeadlineExceeded
	h.p.mu.Unlock()
	for i := 0; i < 3; i++ {
		h.tick()
		h.advance(time.Minute)
	}
	if len(problems) != 1 || problems[0] != "dead_air" {
		t.Fatalf("problems %v", problems)
	}
	h.p.mu.Lock()
	h.p.playErr = nil
	h.p.mu.Unlock()
	h.tick()
	h.tick()
	if len(problems) != 2 || problems[1] != "dead_air_cleared" {
		t.Fatalf("problems %v", problems)
	}
}

func TestClockSources(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "jukem.db"))
	st.Migrate(context.Background(), dir)
	defer st.Close()
	c := &Clock{store: st, buildTime: time.Now().Add(-time.Hour), bootID: func() string { return "b1" }}
	c.probe = func() (bool, bool) { return false, false }
	if s := c.Status(context.Background(), "UTC"); s.Source != ClockNone || s.Trusted {
		t.Fatalf("got %+v", s)
	}
	c.probe = func() (bool, bool) { return false, true }
	c.probedAt = time.Time{}
	if s := c.Status(context.Background(), "UTC"); s.Source != ClockRTC || !s.Trusted {
		t.Fatalf("got %+v", s)
	}
	// A dead RTC battery reports a date before the build.
	c.buildTime = time.Now().Add(time.Hour)
	c.probedAt = time.Time{}
	if s := c.Status(context.Background(), "UTC"); s.Source != ClockNone {
		t.Fatalf("got %+v", s)
	}
	// Manual setting applies an offset and survives a reload on the same boot.
	entered := time.Now().Add(2 * time.Hour)
	if err := c.SetManual(context.Background(), entered); err != nil {
		t.Fatal(err)
	}
	if d := c.Now().Sub(entered); d < -time.Second || d > time.Second {
		t.Fatalf("offset not applied: %v", d)
	}
	c2 := newClock(context.Background(), st, c.buildTime, c.probe, func() string { return "b1" })
	if c2.manual == nil {
		t.Fatal("manual offset not reloaded on the same boot")
	}
	if c3 := newClock(context.Background(), st, c.buildTime, c.probe, func() string { return "b2" }); c3.manual != nil {
		t.Fatal("manual offset kept across boots")
	}
	c.manual = c2.manual
	// NTP synchronisation drops the manual offset.
	c.probe = func() (bool, bool) { return true, false }
	c.probedAt = time.Time{}
	if s := c.Status(context.Background(), "UTC"); s.Source != ClockNTP || c.manual != nil {
		t.Fatalf("got %+v", s)
	}
	var m manualOffset
	if ok, _ := st.GetState(context.Background(), manualOffsetKey, &m); ok {
		t.Fatal("stored offset not removed after sync")
	}
}

func TestExpiredOverrideIsRemoved(t *testing.T) {
	h := newHarness(t)
	h.addRule(t, "Morning", "09:00", "11:00", "Morning")
	h.advance(90 * time.Minute) // 09:30
	h.tick()
	ctx := context.Background()
	if err := h.s.CreateOverride(ctx, store.Override{Mode: "until_next", Intent: "pause", Source: "web UI"}); err != nil {
		t.Fatal(err)
	}
	h.tick()
	// The window ends at 11:00, and with it the override. A day later the
	// Monday window has left the rolling cache; the override must not
	// come back with a new end.
	h.advance(25 * time.Hour)
	h.s.Invalidate()
	h.tick()
	if _, ok, _ := h.st.GetOverride(ctx); ok {
		t.Fatal("expired override still stored")
	}
	if o := h.s.Owner(ctx); o.State != player.OwnerScheduled {
		t.Fatalf("owner %+v", o)
	}
}
