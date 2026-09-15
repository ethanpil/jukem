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
	"jukem/internal/mpdctl"
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

	Store   *store.Store
	Devices *audio.Manager
	Mixer   *audio.Mixer
	MPD     *mpdctl.Supervisor
	Pool    *mpdctl.Pool

	mu       sync.Mutex
	settings store.Settings
	outputs  []mpdctl.Output
	outputMu sync.Mutex // one applyOutput at a time

	handler http.Handler
	cancel  context.CancelFunc
}

// Build opens the store, discovers devices and starts MPD.
func Build(ctx context.Context, cfg config.Config, version string, log *slog.Logger) (*App, error) {
	if err := checkDataDir(cfg.DataDir); err != nil {
		return nil, &MaintenanceError{
			Reason: fmt.Sprintf("The data directory %s is not usable: %v", cfg.DataDir, err),
			Fix:    dataDirFix(cfg.DataDir),
		}
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

	srv, err := api.New(api.Options{Version: version, Static: web.Files, Store: db, Health: a.Health, TLS: settings.HTTPSEnabled})
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
}

// onMPDEvent applies the selected output each time MPD starts.
func (a *App) onMPDEvent(ev mpdctl.Event) {
	switch ev.Kind {
	case mpdctl.EventStarted:
		go a.applyOutput()
	case mpdctl.EventUnstable:
		a.log.Error("mpd keeps failing", "error", ev.Err)
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

// Health assembles the health report.
func (a *App) Health() watchdog.Report {
	checks := []watchdog.Check{{Name: "Service", Status: watchdog.StatusOK, Summary: "running " + a.Version}}

	mpd := a.MPD.Status()
	switch {
	case mpd.Running:
		checks = append(checks, watchdog.Check{Name: "MPD", Status: watchdog.StatusOK, Summary: "running " + time.Since(mpd.Since).Truncate(time.Second).String()})
	default:
		checks = append(checks, watchdog.Check{Name: "MPD", Status: watchdog.StatusError, Summary: "not running: " + mpd.LastError,
			Fix: "jukem restarts MPD on its own. If this continues, check that the mpd package is installed and read the log."})
	}

	snap := a.Devices.Snapshot()
	switch {
	case snap.Saved == nil:
		checks = append(checks, watchdog.Check{Name: "Audio", Status: watchdog.StatusWarning, Summary: "no output selected", Fix: "Choose an output in Settings > Audio."})
	case snap.Selected == nil:
		checks = append(checks, watchdog.Check{Name: "Audio", Status: watchdog.StatusError, Summary: "selected output " + snap.Saved.Name + " is missing",
			Fix: "Plug the device in again, or choose another output in Settings > Audio."})
	default:
		summary := "OK, " + snap.Selected.Name
		if !snap.SysReadable {
			summary += " (matched by card ID only: /sys is not readable)"
		}
		checks = append(checks, watchdog.Check{Name: "Audio", Status: watchdog.StatusOK, Summary: summary})
	}
	return watchdog.Report{Status: watchdog.Worst(checks), Checks: checks}
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
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
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
