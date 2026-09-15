// Package app wires the components of the service together.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"jukem/internal/api"
	"jukem/internal/audio"
	"jukem/internal/config"
	"jukem/internal/events"
	"jukem/internal/library"
	"jukem/internal/mpdctl"
	"jukem/internal/player"
	"jukem/internal/scheduler"
	"jukem/internal/store"
	"jukem/internal/watchdog"
	"jukem/web"
)

// MaintenanceError carries the reason and fix shown on the maintenance
// page. Build returns it for every problem that must not crash-loop.
type MaintenanceError struct {
	Reason string
	Fix    string
}

func (e *MaintenanceError) Error() string { return e.Reason }

// App holds the running components.
type App struct {
	Version string
	cfg     config.Config
	log     *slog.Logger

	Store     *store.Store
	Devices   *audio.Manager
	Mixer     *audio.Mixer
	MPD       *mpdctl.Supervisor
	Pool      *mpdctl.Pool
	Player    *player.Player
	Events    *events.Hub
	Library   *library.Browser
	Files     *library.Files
	Playlists *library.Playlists
	Scheduler *scheduler.Scheduler
	Clock     *scheduler.Clock
	Alerter   *watchdog.Alerter
	// Restart stops the service cleanly; the command sets it.
	Restart func()

	mu               sync.Mutex
	settings         store.Settings
	outputs          []mpdctl.Output
	rescanAfterStart bool       // a music root change needs a full scan
	outputMu         sync.Mutex // one applyOutput at a time

	handler http.Handler
	cancel  context.CancelFunc

	// The health report is cached for a moment: GET /status, /healthz
	// and the Docker health check all ask for it.
	healthMu sync.Mutex
	healthAt time.Time
	health   watchdog.Report
}

// Build opens the store, discovers devices and starts MPD.
func Build(ctx context.Context, cfg config.Config, version string, buildTime time.Time, log *slog.Logger) (*App, error) {
	if err := checkDataDir(cfg.DataDir); err != nil {
		return nil, &MaintenanceError{
			Reason: fmt.Sprintf("The data directory %s is not usable: %v", cfg.DataDir, err),
			Fix:    dataDirFix(cfg.DataDir),
		}
	}
	// An older release kept an uploaded certificate and its private key
	// here. jukem no longer uses them, so the owner removes them.
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "tls")); err == nil {
		log.Warn("the data directory holds TLS files from an older release; jukem no longer serves HTTPS, so remove them", "path", filepath.Join(cfg.DataDir, "tls"))
	}
	db, err := openStore(ctx, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	settings, err := db.LoadSettings(ctx)
	if err != nil {
		db.Close()
		return nil, &MaintenanceError{Reason: "The settings do not load: " + err.Error(), Fix: "Check the log, then restart the service."}
	}

	ctx, cancel := context.WithCancel(ctx)
	a := &App{Version: version, cfg: cfg, log: log, Store: db, settings: settings, cancel: cancel, Mixer: audio.NewMixer()}
	a.Devices = audio.NewManager(log)
	paths := mpdctl.PathsFor(cfg.DataDir)
	a.Pool = mpdctl.NewPool(paths.Socket, 3)
	a.Events = events.New()
	// The queue generation survives a restart, so an override made
	// against the queue still applies after an upgrade.
	var gen int64
	db.GetState(ctx, "queue_generation", &gen)
	a.Player = player.New(a.Pool, a.volumeLimits, gen, func(g int64) {
		if err := db.SetState(context.Background(), "queue_generation", g); err != nil {
			log.Warn("cannot persist the queue generation", "error", err)
		}
	})
	a.Library = library.NewBrowser(a.Pool)
	a.Files = library.NewFiles(func() string { return a.Settings().MusicRoot }, a.uploadLimits, a.Player, a.Events, log, config.Runtime())
	a.Playlists = library.NewPlaylists(paths.PlaylistDir, func() string { return a.Settings().MusicRoot }, db)
	a.Clock = scheduler.NewClock(ctx, db, buildTime)
	a.Alerter = watchdog.NewAlerter(db, a.Events, log, func() watchdog.Webhook {
		set := a.Settings()
		return watchdog.Webhook{URL: set.AlertWebhookURL, Preset: set.AlertWebhookPreset}
	})
	a.Scheduler = scheduler.New(ctx, scheduler.Deps{
		Store: db, Player: a.Player, Events: a.Events, Clock: a.Clock, Log: log,
		Settings:      a.Settings,
		Resolve:       a.resolveProgram,
		MPDRunning:    func() bool { return a.MPD.Status().Running },
		DevicePresent: func() bool { return a.Devices.Snapshot().Selected != nil },
		OnProblem:     a.onSchedulerProblem,
	})
	a.MPD = mpdctl.New(cfg.DataDir, "mpd", log, a.onMPDEvent)

	// The first scan runs before MPD starts, so the config lists every
	// present device from the beginning.
	if err := a.Devices.SetSaved(ctx, settings.OutputDevice); err != nil {
		log.Warn("audio device scan failed", "error", err)
	}
	snap := a.Devices.Snapshot()
	a.outputs = outputsFor(snap.Devices)
	if err := a.MPD.Start(ctx, mpdctl.NewConfig(cfg.DataDir, settings.MusicRoot, a.outputs)); err != nil {
		cancel()
		db.Close()
		return nil, &MaintenanceError{Reason: "MPD does not start: " + err.Error(), Fix: "Check that the mpd package is installed and the data directory is writable, then restart the service."}
	}
	// Later changes go through onDevices, which is registered only now so
	// that the first scan does not act before MPD exists.
	a.Devices.OnChange = a.onDevices
	go a.Devices.Run(ctx)
	go a.watchMPD(ctx)
	go a.Files.Run(ctx)
	go a.Scheduler.Run(ctx)
	go a.runWatchdog(ctx)
	go a.runNightly(ctx)

	srv, err := api.New(api.Options{
		Version: version, Static: web.Files, Store: db, Health: a.Health, TrustedProxies: cfg.TrustedProxies,
		Player: a.Player, Events: a.Events, Devices: a.Devices, Mixer: a.Mixer, Library: a.Library, Files: a.Files, Playlists: a.Playlists, Scheduler: a.Scheduler, Clock: a.Clock,
		Owner: a.ownerNow, Transport: a.Transport, PlayEntry: a.PlayEntry, QueueAction: a.QueueAction, SelectOutput: a.SelectOutput,
		Settings: a.Settings, UpdateSettings: a.UpdateSettings,
		Alerter: a.Alerter, Snapshot: a.Snapshot, Restart: a.RequestRestart,
	})
	if err != nil {
		a.Close()
		return nil, err
	}
	a.handler = srv.Handler()
	return a, nil
}

// Handler returns the HTTP handler.
func (a *App) Handler() http.Handler { return a.handler }

// Close stops MPD and closes the store.
func (a *App) Close() {
	a.cancel()
	a.MPD.Stop()
	a.Pool.Close()
	a.Store.Close()
}

// Settings returns a copy of the current settings.
func (a *App) Settings() store.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// outputsFor lists one audio_output per present device.
func outputsFor(devices []audio.Device) []mpdctl.Output {
	var outs []mpdctl.Output
	for _, d := range devices {
		outs = append(outs, mpdctl.Output{Name: d.OutputName(), Device: d.ALSAName()})
	}
	return outs
}

func sameOutputs(a, b []mpdctl.Output) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// onDevices runs after every device change. A new set of outputs means a
// new config and an MPD restart; a change of selection alone is instant.
func (a *App) onDevices(snap audio.Snapshot) {
	outputs := outputsFor(snap.Devices)
	a.mu.Lock()
	changed := !sameOutputs(a.outputs, outputs)
	musicRoot := a.settings.MusicRoot
	a.mu.Unlock()
	if !changed {
		go a.applyOutput()
		return
	}
	a.log.Info("audio devices changed, restarting mpd", "outputs", len(outputs))
	if err := a.MPD.Reconfigure(mpdctl.NewConfig(a.cfg.DataDir, musicRoot, outputs)); err != nil {
		// The outputs stay as they were, so the next change tries again.
		a.log.Error("cannot reconfigure mpd", "error", err)
		return
	}
	a.mu.Lock()
	a.outputs = outputs
	a.mu.Unlock()
	a.Events.Publish(events.Devices, "")
}

// onMPDEvent applies the selected output each time MPD starts.
func (a *App) onMPDEvent(ev mpdctl.Event) {
	switch ev.Kind {
	case mpdctl.EventStarted:
		a.Files.ResetScan()
		go func() {
			a.applyOutput()
			a.applyPlayerSettings()
			a.Events.Publish(events.Player, "")
			a.Events.Publish(events.Health, "")
			a.Scheduler.Kick()
		}()
	case mpdctl.EventExited, mpdctl.EventStopped:
		a.Events.Publish(events.Player, "")
		a.Events.Publish(events.Health, "")
		a.Scheduler.Kick()
	case mpdctl.EventUnstable:
		a.log.Error("mpd keeps failing", "error", ev.Err)
		a.Alerter.Raise(context.Background(), "mpd_unstable", "MPD keeps failing ("+ev.Err.Error()+"). jukem keeps restarting it.",
			"Read the log for MPD's own message. A missing or busy output device is the usual cause.")
	}
}

// applyOutput enables the selected output, disables the others, and
// reapplies a remembered hardware level. Calls run one at a time, so two
// callers cannot interleave their enable and disable commands.
func (a *App) applyOutput() {
	a.outputMu.Lock()
	defer a.outputMu.Unlock()
	snap := a.Devices.Snapshot()
	name := ""
	if snap.Selected != nil {
		name = snap.Selected.OutputName()
	}
	err := mpdctl.EnableOnly(a.Pool, name)
	if err != nil {
		// MPD can still be busy right after a start; one more try covers it.
		time.Sleep(time.Second)
		err = mpdctl.EnableOnly(a.Pool, name)
	}
	if err != nil {
		a.log.Warn("cannot select mpd output", "output", name, "error", err)
	}
	if snap.Selected == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m, ok, err := a.Store.GetDeviceMixer(ctx, snap.Selected.Key())
	if err != nil || !ok || !m.Reapply {
		return
	}
	if err := a.Mixer.Set(ctx, snap.Selected.CardIndex, m.Control, m.Level); err != nil {
		a.log.Warn("cannot reapply hardware level", "control", m.Control, "error", err)
	}
}

// Health returns the health report, at most two seconds old. The report
// is built outside the lock: MPD can be slow, and a waiting health check
// must not hold the others.
func (a *App) Health() watchdog.Report {
	a.healthMu.Lock()
	if time.Since(a.healthAt) < 2*time.Second {
		defer a.healthMu.Unlock()
		return a.health
	}
	a.healthMu.Unlock()
	rep := a.buildHealth()
	a.healthMu.Lock()
	a.health, a.healthAt = rep, time.Now()
	a.healthMu.Unlock()
	return rep
}

// buildHealth assembles the health report.
func (a *App) buildHealth() watchdog.Report {
	set := a.Settings()
	loc := set.Location()
	checks := []watchdog.Check{{Name: "Service", Status: watchdog.StatusOK, Summary: "running " + a.Version}}

	mpd := a.MPD.Status()
	switch {
	case mpd.Running:
		checks = append(checks, watchdog.Check{Name: "MPD", Status: watchdog.StatusOK, Summary: "running " + since(time.Since(mpd.Since))})
	default:
		checks = append(checks, watchdog.Check{Name: "MPD", Status: watchdog.StatusError, Summary: "not running: " + mpd.LastError,
			Fix: "jukem restarts MPD on its own. If this continues, check that the mpd package is installed and read the log."})
	}

	owner := a.ownerNow()
	switch owner.State {
	case player.OwnerUnavailable:
		// The MPD and Audio lines carry the error itself; a warning here
		// keeps a missing device from a permanent 503.
		checks = append(checks, watchdog.Check{Name: "Scheduler", Status: watchdog.StatusWarning, Summary: "waiting: " + owner.Reason})
	case player.OwnerManual:
		checks = append(checks, watchdog.Check{Name: "Scheduler", Status: watchdog.StatusWarning, Summary: "off, manual mode", Fix: "Switch the scheduler on in Settings > Schedule when the appliance should follow the schedule."})
	default:
		checks = append(checks, watchdog.Check{Name: "Scheduler", Status: watchdog.StatusOK, Summary: owner.Reason})
	}
	clock := a.Clock.Status(context.Background(), set.TimeZone)
	switch clock.Source {
	case scheduler.ClockNTP:
		checks = append(checks, watchdog.Check{Name: "Clock", Status: watchdog.StatusOK, Summary: "synchronized (NTP)"})
	case scheduler.ClockRTC:
		checks = append(checks, watchdog.Check{Name: "Clock", Status: watchdog.StatusOK, Summary: "hardware clock (RTC), not synchronized"})
	case scheduler.ClockManual:
		checks = append(checks, watchdog.Check{Name: "Clock", Status: watchdog.StatusWarning, Summary: "set by hand for this boot", Fix: "Fix the system clock properly: " + clock.FixHint})
	default:
		checks = append(checks, watchdog.Check{Name: "Clock", Status: watchdog.StatusError, Summary: "not set, nothing is scheduled", Fix: "Connect the network for NTP, add an RTC, or set the time in Settings > Schedule."})
	}

	snap := a.Devices.Snapshot()
	switch {
	case snap.Saved == nil:
		checks = append(checks, watchdog.Check{Name: "Audio", Status: watchdog.StatusWarning, Summary: "no output selected", Fix: "Choose an output in Settings > Audio."})
	case snap.Selected == nil:
		checks = append(checks, watchdog.Check{Name: "Audio", Status: watchdog.StatusWarning, Summary: "selected output " + snap.Saved.Name + " is missing",
			Fix: "Plug the device in again, or choose another output in Settings > Audio."})
	default:
		summary := "OK, " + snap.Selected.Name
		if !snap.SysReadable {
			summary += " (matched by card ID only: /sys is not readable)"
		}
		checks = append(checks, watchdog.Check{Name: "Audio", Status: watchdog.StatusOK, Summary: summary})
	}
	checks = append(checks, a.libraryChecks(set, loc)...)
	return watchdog.Report{Status: watchdog.Worst(checks), Checks: checks}
}

// libraryChecks reports the library, the storage and the last track
// change.
func (a *App) libraryChecks(set store.Settings, loc *time.Location) []watchdog.Check {
	var checks []watchdog.Check
	if songs, updated, err := a.Player.Stats(); err == nil {
		summary := fmt.Sprintf("OK, %s tracks", withCommas(songs))
		if !updated.IsZero() {
			// Today's scan shows the time; an older one adds the weekday.
			layout := "15:04"
			if updated.In(loc).Format("2006-01-02") != time.Now().In(loc).Format("2006-01-02") {
				layout = "Mon 15:04"
			}
			summary += ", last scan " + updated.In(loc).Format(layout)
		}
		st := watchdog.Check{Name: "Library", Status: watchdog.StatusOK, Summary: summary}
		if songs == 0 {
			st.Status, st.Fix = watchdog.StatusWarning, "Upload music in Library, or check the music root in Settings > Library."
		}
		checks = append(checks, st)
	}
	stor := library.Stat(set.MusicRoot)
	switch {
	case stor.Missing:
		checks = append(checks, watchdog.Check{Name: "Storage", Status: watchdog.StatusError, Summary: stor.Problem, Fix: "Check the music root in Settings > Library."})
	default:
		c := watchdog.Check{Name: "Storage", Status: watchdog.StatusOK, Summary: "space unknown"}
		if stor.TotalBytes > 0 {
			used := 100 * (stor.TotalBytes - stor.FreeBytes) / stor.TotalBytes
			c.Summary = fmt.Sprintf("%d%% used, %s free", used, humanBytes(stor.FreeBytes))
		}
		if stor.TotalBytes > 0 && stor.FreeBytes < set.FreeSpaceReserve {
			c.Status, c.Fix = watchdog.StatusWarning, "Below the free space reserve: uploads are refused. Delete music or enlarge the disk."
		}
		if stor.ReadOnly {
			// A read-only mount is a valid setup; uploads are hidden.
			c.Summary += ", read-only"
		}
		checks = append(checks, c)
	}
	if last, ok, err := a.Store.LastHistoryAt(context.Background()); err == nil && ok {
		checks = append(checks, watchdog.Check{Name: "Last track change", Status: watchdog.StatusOK, Summary: since(time.Since(last)) + " ago"})
	} else if err == nil {
		checks = append(checks, watchdog.Check{Name: "Last track change", Status: watchdog.StatusOK, Summary: "nothing played yet"})
	}
	return checks
}

// since writes a duration in two units: "4d 2h", "1h 5m", "3m", "20s".
func since(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
}

// withCommas writes 8412 as 8,412.
func withCommas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// checkDataDir verifies that the data directory exists and is writable.
func checkDataDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errors.New("not a directory")
	}
	if !library.Writable(dir) {
		return errors.New("not writable")
	}
	return nil
}

// dataDirFix tells the operator how to repair the data directory. Under
// Docker the directory is a bind mount, so the command runs on the host.
func dataDirFix(dir string) string {
	if config.Runtime() == "docker" {
		return "On the Docker host, give the bind mount for " + dir + " to the container user:\n" +
			"  chown -R 1000:1000 /srv/jukem/data\n" +
			"or set user: in compose.yaml to the owner of that directory, then restart the container."
	}
	return "Create the directory and give it to the service user:\n" +
		"  install -d -o jukem -g jukem -m 0750 " + dir + "\n" +
		"then restart the service."
}

// openStore opens and migrates the database. A schema the binary does not
// know, or a migration that fails, becomes maintenance mode with the
// rollback steps in the message.
func openStore(ctx context.Context, dataDir string) (*store.Store, error) {
	dbPath := filepath.Join(dataDir, "jukem.db")
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, &MaintenanceError{
			Reason: fmt.Sprintf("The database %s does not open: %v", dbPath, err),
			Fix:    "Check that the data directory is writable by the jukem user and that the disk has space, then restart the service.",
		}
	}
	snapDir := filepath.Join(dataDir, "snapshots")
	// A stop signal must not cut a migration short: the migration completes
	// or rolls back on its own, and the caller stops afterwards.
	err = db.Migrate(context.WithoutCancel(ctx), snapDir)
	if err == nil {
		return db, nil
	}
	db.Close()
	var tooNew *store.ErrSchemaTooNew
	var failed *store.MigrationError
	switch {
	case errors.As(err, &tooNew):
		snapshot, ok := store.LatestSnapshot(snapDir, tooNew.Binary)
		fix := "Install the newer jukem package again."
		if ok {
			fix += " To stay on this release, restore the last snapshot this release understands:\n" + restoreSteps(snapshot, dbPath)
		} else {
			fix += fmt.Sprintf(" No snapshot for schema version %d or older exists in %s, so this release cannot use the database.", tooNew.Binary, snapDir)
		}
		return nil, &MaintenanceError{
			Reason: fmt.Sprintf("A newer jukem wrote the database: schema version %d, this binary knows version %d.", tooNew.Database, tooNew.Binary),
			Fix:    fix,
		}
	case errors.As(err, &failed):
		fix := "Report this problem with the log, then restart the service to try again."
		if failed.Snapshot != "" {
			fix = "The database stays consistent at the previous version. Report this problem with the log. " +
				"To go back to the previous jukem release, install the older package and restore the snapshot:\n" +
				restoreSteps(failed.Snapshot, dbPath)
		}
		return nil, &MaintenanceError{
			Reason: fmt.Sprintf("Database migration %d failed. The store rolled it back: %v", failed.Version, failed.Err),
			Fix:    fix,
		}
	}
	return nil, &MaintenanceError{
		Reason: fmt.Sprintf("The database %s is not ready: %v", dbPath, err),
		Fix:    "Check the log, then restart the service.",
	}
}

// restoreSteps lists the commands that put a snapshot in place of the
// database. The service must be stopped first, and how depends on where
// jukem runs.
func restoreSteps(snapshot, dbPath string) string {
	stop, start := "  rc-service jukem stop\n", "  rc-service jukem start"
	if config.Runtime() == "docker" {
		stop, start = "  docker compose stop jukem   (paths below are inside the container)\n", "  docker compose start jukem"
	}
	return stop +
		fmt.Sprintf("  cp %s %s\n", snapshot, dbPath) +
		fmt.Sprintf("  rm -f %s-wal %s-shm\n", dbPath, dbPath) +
		start
}
