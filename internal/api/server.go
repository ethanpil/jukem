// Package api serves the REST API, the SSE stream and the embedded web UI.
package api

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"jukem/internal/watchdog"
)

// csp is the Content-Security-Policy for every page. No inline scripts or
// styles are possible; data: covers the small SVGs in Bootstrap's CSS.
const csp = "default-src 'self'; img-src 'self' data:; media-src 'self'; connect-src 'self'; frame-ancestors 'none'"

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
// job, 503 otherwise. It carries no detail; /api/v1/health does.
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

// NewMaintenance returns the handler used in maintenance mode. It serves the
// maintenance page at every path, the health detail as JSON, and answers
// /healthz with 503. Nothing else is reachable.
func NewMaintenance(static fs.FS, version, reason, fix string) (http.Handler, error) {
	sh, err := newStaticHandler(static, version)
	if err != nil {
		return nil, err
	}
	page, err := fs.ReadFile(static, "maintenance.html")
	if err != nil {
		return nil, err
	}
	report := func() watchdog.Report {
		return watchdog.Report{
			Status:      watchdog.StatusError,
			Maintenance: true,
			Reason:      reason,
			Fix:         fix,
			Checks: []watchdog.Check{{
				Name: "Service", Status: watchdog.StatusError, Summary: reason, Fix: fix,
			}},
		}
	}
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", healthz(report))
	mux.Handle("GET /api/v1/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(report())
	}))
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]any{
			"title": "Service Unavailable", "status": 503,
			"detail": "jukem is in maintenance mode: " + reason,
		})
	}))
	mux.Handle("/app/", sh)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write(page)
	}))
	return securityHeaders(mux), nil
}
