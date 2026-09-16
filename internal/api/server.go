// Package api serves the REST API, the SSE stream and the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/netip"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"jukem/internal/audio"
	"jukem/internal/config"
	"jukem/internal/events"
	"jukem/internal/library"
	"jukem/internal/player"
	"jukem/internal/scheduler"
	"jukem/internal/store"
	"jukem/internal/update"
	"jukem/internal/watchdog"
)

// csp is the Content-Security-Policy for every page. Inline scripts and
// styles are not possible; data: covers the small SVGs in Bootstrap's CSS.
const csp = "default-src 'self'; img-src 'self' data:; media-src 'self'; connect-src 'self'; frame-ancestors 'none'"

const apiPrefix = "/api/v1"

// Options configures the normal server. The function fields are the
// application operations that touch more than one component.
type Options struct {
	Version string
	Static  fs.FS
	Store   *store.Store
	Health  func() watchdog.Report
	// TrustedProxies are the reverse proxies whose forwarding headers
	// jukem reads.
	TrustedProxies []netip.Prefix

	Player    *player.Player
	Events    *events.Hub
	Devices   *audio.Manager
	Mixer     *audio.Mixer
	Library   *library.Browser
	Files     *library.Files
	Playlists *library.Playlists
	Scheduler *scheduler.Scheduler
	Clock     *scheduler.Clock
	// Owner reports who decides playback now.
	Owner func() player.Owner
	// Transport runs play, pause or stop for a caller, creating an override
	// when the scheduler is on.
	Transport func(ctx context.Context, action, source string) error
	// PlayEntry plays from a queue entry, like Transport("play").
	PlayEntry func(ctx context.Context, id int, source string) error
	// QueueAction resolves the source and applies the action.
	QueueAction func(ctx context.Context, a QueueAction, source string) (QueueResult, error)
	// SelectOutput saves and applies an output selection.
	SelectOutput func(ctx context.Context, id *audio.Identity) error
	// UpdateStatus reports the last check for a new release, and
	// CheckUpdate asks GitHub now.
	UpdateStatus func(ctx context.Context) update.Result
	CheckUpdate  func(ctx context.Context) (update.Result, error)

	// Settings returns the current settings; UpdateSettings validates,
	// stores and applies new ones.
	Settings       func() store.Settings
	UpdateSettings func(ctx context.Context, set store.Settings) error

	// PlayAnnouncement starts one announcement for a test from the UI. It
	// answers when the announcement starts, not when it ends. A zero due
	// time does not change the schedule of the announcement.
	PlayAnnouncement func(ctx context.Context, a store.Announcement, due time.Time) error

	// Alerter records and dismisses alerts. The functions below are the
	// maintenance operations of the running application.
	Alerter  *watchdog.Alerter
	Snapshot func(ctx context.Context) (string, error)
	Restart  func() error
}

// Server is the HTTP surface in normal operation.
type Server struct {
	opts    Options
	store   *store.Store
	auth    *auth
	started time.Time
	handler http.Handler
}

// New builds the handler for normal operation.
func New(opts Options) (*Server, error) {
	sh, err := newStaticHandler(opts.Static, opts.Version, "index.html", http.StatusOK)
	if err != nil {
		return nil, err
	}
	s := &Server{
		opts:    opts,
		store:   opts.Store,
		auth:    &auth{store: opts.Store, limiter: newLoginLimiter(), hashSem: make(chan struct{}, hashSlots), proxies: opts.TrustedProxies},
		started: time.Now(),
	}

	apiMux := http.NewServeMux()
	cfg := huma.DefaultConfig("jukem", opts.Version)
	cfg.Info.Description = "Jukebox appliance API. Browsers use a session cookie plus the X-CSRF-Token header; programs send Authorization: Bearer <key>."
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"apiKey":  {Type: "http", Scheme: "bearer"},
		"session": {Type: "apiKey", In: "cookie", Name: sessionCookieName},
	}
	cfg.Security = []map[string][]string{{"apiKey": {}}, {"session": {}}}
	hapi := humago.NewWithPrefix(apiMux, apiPrefix, cfg)
	s.registerSystem(hapi)
	s.registerAuth(hapi)
	if opts.Player != nil {
		s.registerPlayer(hapi)
		s.registerDevices(hapi)
		s.registerSettings(hapi)
	}
	if opts.Library != nil {
		s.registerLibrary(hapi, apiMux)
	}
	if opts.Files != nil {
		s.registerUpload(hapi, apiMux)
	}
	if opts.Playlists != nil {
		s.registerPlaylists(hapi)
		s.registerFileOps(hapi)
	}
	if opts.Scheduler != nil {
		s.registerSchedule(hapi)
	}
	// The announcements need only the store; the test play needs the app.
	s.registerAnnouncements(hapi)
	if opts.Alerter != nil {
		s.registerSystemOps(hapi)
	}
	if opts.Events != nil {
		apiMux.Handle("GET "+apiPrefix+"/events", opts.Events)
		hapi.OpenAPI().AddOperation(&huma.Operation{
			OperationID: "events", Method: http.MethodGet, Path: "/events", Tags: []string{"system"},
			Summary:     "Server-Sent Events change notifications",
			Description: "Each event is a JSON object with a type: player, queue, library, devices, schedule, alerts, settings, health or upload. Refetch the resource that changed.",
			Responses:   map[string]*huma.Response{"200": {Description: "text/event-stream"}},
		})
	}
	apiMux.Handle(apiPrefix+"/", problemHandler(http.StatusNotFound, "no such endpoint"))

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", healthz(opts.Health))
	mux.Handle("/api/", s.cors(s.auth.middleware(apiMux)))
	mux.Handle("/", sh)
	s.handler = securityHeaders(mux)
	return s, nil
}

// cors answers cross-origin API requests from the origins in the
// settings. With no origin listed, the browser's same-origin rule stands.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.opts.Settings != nil && slices.Contains(s.opts.Settings().CORSOrigins, origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-CSRF-Token")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.handler }

// SystemInfo is the version detail.
type SystemInfo struct {
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Runtime       string `json:"runtime" enum:"host,docker"`
}

func (s *Server) registerSystem(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-health", Method: http.MethodGet, Path: "/health", Tags: []string{"system"},
		Summary: "Full health detail", Security: []map[string][]string{},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body watchdog.Report }, error) {
		return &struct{ Body watchdog.Report }{Body: s.opts.Health()}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-system-info", Method: http.MethodGet, Path: "/system/info", Tags: []string{"system"},
		Summary: "Version, schema version, uptime and runtime", Security: []map[string][]string{},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body SystemInfo }, error) {
		v, err := s.store.Version(ctx)
		if err != nil {
			return nil, err
		}
		return &struct{ Body SystemInfo }{Body: SystemInfo{
			Version:       s.opts.Version,
			SchemaVersion: v,
			UptimeSeconds: int64(time.Since(s.started).Seconds()),
			Runtime:       config.Runtime(),
		}}, nil
	})
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
		writeProblem(w, status, detail)
	})
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"title": http.StatusText(status), "status": status, "detail": detail,
	})
}
