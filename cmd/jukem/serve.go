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
	"syscall"
	"time"

	"jukem/internal/api"
	"jukem/internal/config"
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
		// The message must still go somewhere when the log file cannot be
		// opened; stderr reaches the supervisor.
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
		return serveMaintenance(ctx, cfg, cfgErr.Error(),
			fmt.Sprintf("Fix the syntax in %s and restart the service.", *cfgPath))
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
			Fix:    fmt.Sprintf("Create it and give it to the service user:\n  install -d -o jukem -g jukem -m 0750 %s\nthen restart the service.", cfg.DataDir),
		}
	}
	health := func() watchdog.Report {
		checks := []watchdog.Check{{Name: "Service", Status: watchdog.StatusOK, Summary: "running " + version}}
		return watchdog.Report{Status: watchdog.Worst(checks), Checks: checks}
	}
	srv, err := api.New(api.Options{Version: version, Static: web.Files, Health: health})
	if err != nil {
		return nil, nil, err
	}
	return srv.Handler(), func() {}, nil
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

func serveMaintenance(ctx context.Context, cfg config.Config, reason, fix string) error {
	slog.Error("entering maintenance mode", "reason", reason)
	h, err := api.NewMaintenance(web.Files, version, reason, fix)
	if err != nil {
		return err
	}
	return serveHTTP(ctx, cfg.Listen, h, slog.Default())
}

// serveHTTP runs the listener until ctx is cancelled, then drains for a few
// seconds.
func serveHTTP(ctx context.Context, listen string, h http.Handler, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", listen, err)
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
