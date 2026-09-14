// Package api serves the REST API, the SSE stream and the embedded web UI.
package api

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"jukem/internal/watchdog"
)

// csp is the Content-Security-Policy for every page. Inline scripts and
// styles are not possible; data: covers the small SVGs in Bootstrap's CSS.
const csp = "default-src 'self'; img-src 'self' data:; media-src 'self'; connect-src 'self'; frame-ancestors 'none'"

// Options configures the normal server.
type Options struct {
	Version string
	Static  fs.FS
	Health  func() watchdog.Report
}

// New builds the handler for normal operation.
func New(opts Options) (http.Handler, error) {
	sh, err := newStaticHandler(opts.Static, opts.Version, "index.html", http.StatusOK)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", healthz(opts.Health))
	mux.Handle("GET /api/v1/health", healthJSON(opts.Health))
	mux.Handle("/api/", problemHandler(http.StatusNotFound, "no such endpoint"))
	mux.Handle("/", sh)
	return securityHeaders(mux), nil
}

// NewMaintenance returns the handler for maintenance mode. It serves the
// maintenance page at every path, the health detail as JSON, and answers
// /healthz with 503. No other endpoint is reachable.
func NewMaintenance(static fs.FS, version, reason, fix string) (http.Handler, error) {
	sh, err := newStaticHandler(static, version, "maintenance.html", http.StatusServiceUnavailable)
	if err != nil {
		return nil, err
	}
	report := watchdog.Report{
		Status:      watchdog.StatusError,
		Maintenance: true,
		Reason:      reason,
		Fix:         fix,
		Checks:      []watchdog.Check{{Name: "Service", Status: watchdog.StatusError, Summary: "maintenance mode"}},
	}
	health := func() watchdog.Report { return report }
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", healthz(health))
	mux.Handle("GET /api/v1/health", healthJSON(health))
	mux.Handle("/api/", problemHandler(http.StatusServiceUnavailable, "jukem is in maintenance mode: "+reason))
	mux.Handle("/", sh)
	return securityHeaders(mux), nil
}

// securityHeaders adds the headers every response carries.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// healthz answers container health checks: 200 when the service can do its
// job, 503 if not. It carries no detail; /api/v1/health does.
func healthz(health func() watchdog.Report) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		rep := health()
		if rep.Maintenance || rep.Status == watchdog.StatusError {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("unavailable\n"))
			return
		}
		w.Write([]byte("ok\n"))
	})
}

// healthJSON serves the full health report.
func healthJSON(health func() watchdog.Report) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(health())
	})
}

// problemHandler answers with an RFC 9457 problem document.
func problemHandler(status int, detail string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{
			"title": http.StatusText(status), "status": status, "detail": detail,
		})
	})
}
