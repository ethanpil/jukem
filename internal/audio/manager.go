package audio

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Snapshot is the device state the manager reports.
type Snapshot struct {
	Devices     []Device
	Selected    *Device // present device that matches the saved identity
	Saved       *Identity
	MatchRule   string
	SysReadable bool
	ScannedAt   time.Time
}

// Manager keeps the list of present devices current. It rescans when
// /dev/snd changes, every 60 seconds as a safety net (15 seconds while the
// selected output is missing), and on demand.
type Manager struct {
	log      *slog.Logger
	onChange func(Snapshot)
	aplay    func(ctx context.Context) (string, error)
	sysRoot  string
	devSnd   string

	mu    sync.Mutex
	snap  Snapshot
	saved *Identity
	kick  chan struct{}
}

// NewManager creates a manager. onChange runs after every rescan that
// changes the device set or the selected device's presence.
func NewManager(log *slog.Logger, onChange func(Snapshot)) *Manager {
	return &Manager{
		log:      log,
		onChange: onChange,
		aplay:    runAplay,
		sysRoot:  "/sys",
		devSnd:   "/dev/snd",
		kick:     make(chan struct{}, 1),
	}
}

func runAplay(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "aplay", "-l")
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		// aplay exits 1 with "no soundcards found" when nothing is present,
		// which is a valid empty result.
		if strings.Contains(string(out), "no soundcards") {
			return "", nil
		}
		return "", fmt.Errorf("aplay -l: %w", err)
	}
	return string(out), nil
}

// SetSaved sets the identity to look for and rescans.
func (m *Manager) SetSaved(id *Identity) {
	m.mu.Lock()
	m.saved = id
	m.mu.Unlock()
	m.Rescan(context.Background())
}

// Snapshot returns the last scan result.
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap
}

// Rescan lists the devices now and reports a change to onChange.
func (m *Manager) Rescan(ctx context.Context) error {
	out, err := m.aplay(ctx)
	if err != nil {
		return err
	}
	devices := ParseAplay(out)
	readable := sysReadable(m.sysRoot)
	if readable {
		for i := range devices {
			enrich(&devices[i], m.sysRoot)
		}
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].CardIndex != devices[j].CardIndex {
			return devices[i].CardIndex < devices[j].CardIndex
		}
		return devices[i].Device < devices[j].Device
	})

	m.mu.Lock()
	snap := Snapshot{Devices: devices, Saved: m.saved, SysReadable: readable, ScannedAt: time.Now()}
	if m.saved != nil {
		snap.Selected, snap.MatchRule = Match(*m.saved, devices)
	}
	changed := !sameDevices(m.snap.Devices, devices) ||
		(m.snap.Selected == nil) != (snap.Selected == nil) ||
		(m.snap.Selected != nil && snap.Selected != nil && m.snap.Selected.Key() != snap.Selected.Key())
	m.snap = snap
	m.mu.Unlock()

	if changed && m.onChange != nil {
		m.onChange(snap)
	}
	return nil
}

func sameDevices(a, b []Device) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key() != b[i].Key() || a[i].CardIndex != b[i].CardIndex || a[i].CardID != b[i].CardID {
			return false
		}
	}
	return true
}

// Trigger asks the background loop for a rescan soon.
func (m *Manager) Trigger() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run watches /dev/snd and rescans on a timer until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	if err := watchDevSnd(ctx, m.devSnd, m.Trigger); err != nil {
		m.log.Warn("cannot watch /dev/snd, using the timer only", "error", err)
	}
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.kick:
			// A card's nodes appear one at a time over a few hundred
			// milliseconds; wait for the last event before reading.
			debounce.Reset(time.Second)
		case <-debounce.C:
			if err := m.Rescan(ctx); err != nil {
				m.log.Warn("device rescan failed", "error", err)
			}
		case <-time.After(m.interval()):
			if err := m.Rescan(ctx); err != nil {
				m.log.Warn("device rescan failed", "error", err)
			}
		}
	}
}

func (m *Manager) interval() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saved != nil && m.snap.Selected == nil {
		return 15 * time.Second
	}
	return 60 * time.Second
}
