package main

import (
	"context"
	"crypto/tls"
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

	a, err := app.Build(ctx, cfg, version, buildStamp(), logger)
	if err != nil {
		var mm *app.MaintenanceError
		if errors.As(err, &mm) {
			return serveMaintenance(ctx, cfg, mm.Reason, mm.Fix)
		}
		return err
	}
	defer a.Close()
	// A restart from the UI ends the servers; the supervisor starts jukem
	// again. The short delay lets the answer reach the browser.
	sctx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	a.Restart = func() { time.AfterFunc(500*time.Millisecond, stopServing) }
	if a.ServeTLS() {
		return serveTLS(sctx, cfg.Listen, cfg.ListenTLS, a.LoadTLS, a.Handler(), logger)
	}
	if a.Settings().HTTPSEnabled {
		logger.Warn("HTTPS is switched on but no valid certificate is stored, serving HTTP")
	}
	return serveHTTP(sctx, cfg.Listen, a.Handler(), logger)
}

// buildStamp returns the build time from the linker, or the binary's
// modification time as the fallback for a development build.
func buildStamp() time.Time {
	if t, err := time.Parse(time.RFC3339, buildTime); err == nil {
		return t
	}
	if exe, err := os.Executable(); err == nil {
		if st, err := os.Stat(exe); err == nil {
			return st.ModTime()
		}
	}
	return time.Time{}
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
	return runServers(ctx, logger, server{ln: ln, h: h})
}

// serveTLS serves the application over TLS and redirects plain HTTP to
// it. The plain port still answers /healthz itself, for the container
// health check. The redirect keeps the host name. It adds the TLS port
// when that is not 443, except in Docker, where the published port is
// 443 and the container port is not.
func serveTLS(ctx context.Context, listen, listenTLS string, load func() (*tls.Certificate, error), h http.Handler, logger *slog.Logger) error {
	tlsLn, err := listenWithRetry(ctx, listenTLS, logger)
	if err != nil {
		return err
	}
	plainLn, err := listenWithRetry(ctx, listen, logger)
	if err != nil {
		tlsLn.Close()
		return err
	}
	_, port, _ := net.SplitHostPort(tlsLn.Addr().String())
	redirect := http.NewServeMux()
	redirect.Handle("GET /healthz", h)
	redirect.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		target := "https://" + host
		if port != "443" && config.Runtime() != "docker" {
			target = "https://" + net.JoinHostPort(host, port)
		}
		http.Redirect(w, r, target+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
	tlsConf := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return load()
		},
	}
	return runServers(ctx, logger, server{ln: tlsLn, h: h, tls: tlsConf}, server{ln: plainLn, h: redirect})
}

// server is one listener with its handler.
type server struct {
	ln  net.Listener
	h   http.Handler
	tls *tls.Config // set for TLS
}

// runServers serves every listener until one fails or ctx ends, then
// drains all of them.
func runServers(ctx context.Context, logger *slog.Logger, servers ...server) error {
	errc := make(chan error, len(servers))
	var running []*http.Server
	for _, s := range servers {
		srv := &http.Server{
			Handler:           s.h,
			TLSConfig:         s.tls,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		running = append(running, srv)
		go func(s server, srv *http.Server) {
			if s.tls != nil {
				errc <- srv.ServeTLS(s.ln, "", "")
				return
			}
			errc <- srv.Serve(s.ln)
		}(s, srv)
		logger.Info("listening", "addr", s.ln.Addr().String(), "tls", s.tls != nil)
	}
	var err error
	select {
	case err = <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	case <-ctx.Done():
		logger.Info("shutting down")
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, srv := range running {
		srv.Shutdown(sctx)
	}
	return err
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
