package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"jukem/internal/api"
	"jukem/internal/config"
	"jukem/internal/store"
	"jukem/internal/watchdog"
	"jukem/web"
)

// runServe starts the service. A fatal problem at startup does not exit: the
// supervisor would restart jukem every two seconds. Instead the service enters
// maintenance mode and explains the problem at the usual address.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "/etc/jukem/config.yaml", "bootstrap config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, warnings, cfgErr := config.Load(*cfgPath)
	logger, closeLog, err := newLogger(cfg.LogFile)
	if err != nil {
		// When the log file does not open, the message must still go
		// somewhere; stderr reaches the supervisor.
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
		logger.Warn("log file unavailable, logging to stderr", "error", err)
	} else {
		defer closeLog.Close()
	}
	slog.SetDefault(logger)
	for _, w := range warnings {
		logger.Warn(w)
	}
	logger.Info("jukem starting", "version", version, "listen", cfg.Listen, "data_dir", cfg.DataDir, "runtime", config.Runtime())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfgErr != nil {
		fix := fmt.Sprintf("Make %s readable by the jukem user and restart the service.", *cfgPath)
		var pe *config.ParseError
		if errors.As(cfgErr, &pe) {
			fix = fmt.Sprintf("Fix the YAML syntax in %s and restart the service.", *cfgPath)
		}
		return serveMaintenance(ctx, cfg, cfgErr.Error(), fix)
	}

	handler, shutdown, err := buildApp(ctx, cfg, logger)
	if err != nil {
		var mm *maintenanceError
		if errors.As(err, &mm) {
			return serveMaintenance(ctx, cfg, mm.Reason, mm.Fix)
		}
		return err
	}
	defer shutdown()
	return serveHTTP(ctx, cfg.Listen, handler, logger)
}

// maintenanceError carries the reason and fix shown on the maintenance page.
type maintenanceError struct {
	Reason string
	Fix    string
}

func (e *maintenanceError) Error() string { return e.Reason }

// buildApp wires the components. It grows with each build step.
func buildApp(ctx context.Context, cfg config.Config, logger *slog.Logger) (http.Handler, func(), error) {
	if err := checkDataDir(cfg.DataDir); err != nil {
		return nil, nil, &maintenanceError{
			Reason: fmt.Sprintf("The data directory %s is not usable: %v", cfg.DataDir, err),
			Fix:    dataDirFix(cfg.DataDir),
		}
	}
	db, err := openStore(ctx, cfg.DataDir)
	if err != nil {
		return nil, nil, err
	}
	health := func() watchdog.Report {
		checks := []watchdog.Check{{Name: "Service", Status: watchdog.StatusOK, Summary: "running " + version}}
		return watchdog.Report{Status: watchdog.Worst(checks), Checks: checks}
	}
	h, err := api.New(api.Options{Version: version, Static: web.Files, Health: health})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return h, func() { db.Close() }, nil
}

// openStore opens and migrates the database. A schema the binary does not
// know, or a migration that fails, becomes maintenance mode with the
// rollback steps in the message.
func openStore(ctx context.Context, dataDir string) (*store.Store, error) {
	dbPath := filepath.Join(dataDir, "jukem.db")
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, &maintenanceError{
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
		return nil, &maintenanceError{
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
		return nil, &maintenanceError{
			Reason: fmt.Sprintf("Database migration %d failed. The store rolled it back: %v", failed.Version, failed.Err),
			Fix:    fix,
		}
	}
	return nil, &maintenanceError{
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

// listenWithRetry opens the port, and tries again every few seconds until
// ctx ends.
func listenWithRetry(ctx context.Context, listen string, logger *slog.Logger) (net.Listener, error) {
	for {
		ln, err := net.Listen("tcp", listen)
		if err == nil {
			return ln, nil
		}
		logger.Error("cannot listen, retrying in 5s", "addr", listen, "error", err)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("listen on %s: %w", listen, err)
		case <-time.After(5 * time.Second):
		}
	}
}

func serveMaintenance(ctx context.Context, cfg config.Config, reason, fix string) error {
	slog.Error("entering maintenance mode", "reason", reason)
	h, err := api.NewMaintenance(web.Files, version, reason, fix)
	if err != nil {
		return err
	}
	return serveHTTP(ctx, cfg.Listen, h, slog.Default())
}

// serveHTTP runs the listener until ctx is cancelled, then drains for a few
// seconds. A port that does not open is retried, not fatal: an exit would
// make the supervisor restart jukem every two seconds.
func serveHTTP(ctx context.Context, listen string, h http.Handler, logger *slog.Logger) error {
	ln, err := listenWithRetry(ctx, listen, logger)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	logger.Info("listening", "addr", ln.Addr().String())
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}
