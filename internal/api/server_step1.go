package api

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"jukem/internal/watchdog"
)

// Options configures the normal server.
type Options struct {
	Version string
	Static  fs.FS
	Health  func() watchdog.Report
}

// Server is the HTTP surface of jukem.
type Server struct {
	mux *http.ServeMux
}

// New builds the server.
func New(opts Options) (*Server, error) {
	sh, err := newStaticHandler(opts.Static, opts.Version)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", healthz(opts.Health))
	mux.Handle("GET /api/v1/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(opts.Health())
	}))
	mux.Handle("/", sh)
	return &Server{mux: mux}, nil
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	return securityHeaders(s.mux)
}
