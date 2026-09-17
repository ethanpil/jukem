package scheduler

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"jukem/internal/store"
)

// ClockSource says where the time comes from.
type ClockSource string

const (
	ClockNTP    ClockSource = "ntp"    // the kernel reports synchronisation
	ClockRTC    ClockSource = "rtc"    // a hardware clock and a plausible date
	ClockManual ClockSource = "manual" // entered by a person during this boot
	ClockNone   ClockSource = "none"   // nothing trusts the time
)

// ClockStatus is the answer to GET /clock.
type ClockStatus struct {
	Source   ClockSource `json:"source" enum:"ntp,rtc,manual,none"`
	Trusted  bool        `json:"trusted" doc:"False means nothing is scheduled"`
	Now      time.Time   `json:"now" doc:"The time jukem schedules against"`
	NowLocal string      `json:"now_local" doc:"The same in the configured zone"`
	TimeZone string      `json:"time_zone"`
	SetAt    *time.Time  `json:"set_at,omitempty" doc:"When a person set the clock"`
	Offset   float64     `json:"offset_seconds,omitempty" doc:"Manual correction applied to the system clock"`
	FixHint  string      `json:"fix_hint,omitempty" doc:"Console command that fixes the system clock properly"`
}

// manualOffset is what a person entered, stored with the boot it belongs
// to. Without an RTC the system clock is arbitrary after a restart, so the
// offset is dropped when the boot id changes.
type manualOffset struct {
	OffsetSeconds float64   `json:"offset_seconds"`
	BootID        string    `json:"boot_id"`
	SetAt         time.Time `json:"set_at"`
}

const manualOffsetKey = "clock_offset"

// Clock decides whether the time can be trusted and applies a manual
// offset. buildTime is the binary's build timestamp. A hardware clock
// that shows an earlier date is wrong.
type Clock struct {
	store     *store.Store
	buildTime time.Time
	probe     func() (ntpSynced bool, rtcPresent bool)
	bootID    func() string

	mu     sync.Mutex
	manual *manualOffset
	// The kernel probe is cached for a moment, because status, health
	// and every tick ask for it.
	probedAt time.Time
	probeNTP bool
	probeRTC bool
}

// probeCached runs the probe at most every two seconds.
func (c *Clock) probeCached() (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.probedAt) < 2*time.Second {
		return c.probeNTP, c.probeRTC
	}
	c.probeNTP, c.probeRTC = c.probe()
	c.probedAt = time.Now()
	return c.probeNTP, c.probeRTC
}

// NewClock loads a stored manual offset when it belongs to this boot.
func NewClock(ctx context.Context, st *store.Store, buildTime time.Time) *Clock {
	return newClock(ctx, st, buildTime, probeClock, readBootID)
}

func newClock(ctx context.Context, st *store.Store, buildTime time.Time, probe func() (bool, bool), bootID func() string) *Clock {
	c := &Clock{store: st, buildTime: buildTime, probe: probe, bootID: bootID}
	var m manualOffset
	if ok, err := st.GetState(ctx, manualOffsetKey, &m); err == nil && ok {
		if m.BootID != "" && m.BootID == c.bootID() {
			c.manual = &m
		} else {
			st.DeleteState(ctx, manualOffsetKey)
		}
	}
	return c
}

// Now returns the time jukem schedules against.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	m := c.manual
	c.mu.Unlock()
	now := time.Now()
	if m != nil {
		now = now.Add(time.Duration(m.OffsetSeconds * float64(time.Second)))
	}
	return now
}

// Status reports the source. As soon as the kernel reports
// synchronisation, a manual offset is dropped.
func (c *Clock) Status(ctx context.Context, zone, layout string) ClockStatus {
	synced, rtc := c.probeCached()
	c.mu.Lock()
	if synced && c.manual != nil {
		c.manual = nil
		c.store.DeleteState(ctx, manualOffsetKey)
	}
	m := c.manual
	c.mu.Unlock()

	st := ClockStatus{TimeZone: zone, Now: c.Now()}
	switch {
	case synced:
		st.Source, st.Trusted = ClockNTP, true
	case m != nil:
		st.Source, st.Trusted = ClockManual, true
		at := m.SetAt
		st.SetAt = &at
		st.Offset = m.OffsetSeconds
	case rtc && time.Now().After(c.buildTime):
		st.Source, st.Trusted = ClockRTC, true
	default:
		st.Source = ClockNone
	}
	if loc, err := time.LoadLocation(zone); err == nil {
		st.NowLocal = st.Now.In(loc).Format("Mon 2 Jan 2006 " + layout)
	}
	if st.Source == ClockManual || st.Source == ClockNone {
		st.FixHint = fmt.Sprintf("date -s '%s' && hwclock -w", st.Now.UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	return st
}

// Trusted reports whether scheduling may run.
func (c *Clock) Trusted(ctx context.Context) bool {
	return c.Status(ctx, "UTC", "15:04").Trusted
}

// SetManual stores the difference between the entered time and the
// system clock, tied to this boot.
func (c *Clock) SetManual(ctx context.Context, entered time.Time) error {
	m := manualOffset{OffsetSeconds: entered.Sub(time.Now()).Seconds(), BootID: c.bootID(), SetAt: time.Now()}
	if err := c.store.SetState(ctx, manualOffsetKey, m); err != nil {
		return err
	}
	c.mu.Lock()
	c.manual = &m
	c.mu.Unlock()
	return nil
}

// readBootID returns the kernel's boot id, or "" where there is none.
func readBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
