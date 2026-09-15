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
	"jukem/internal/app"
	"jukem/internal/config"
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

	a, err := app.Build(ctx, cfg, version, logger)
	if err != nil {
		var mm *app.MaintenanceError
		if errors.As(err, &mm) {
			return serveMaintenance(ctx, cfg, mm.Reason, mm.Fix)
		}
		return err
	}
	defer a.Close()
	return serveHTTP(ctx, cfg.Listen, a.Handler(), logger)
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
