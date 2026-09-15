package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"jukem/internal/store"
)

const (
	sessionCookie = "jukem_session"
	csrfHeader    = "X-CSRF-Token"
	sessionLife   = 30 * 24 * time.Hour
)

// PrincipalKind says how a request authenticated.
type PrincipalKind string

const (
	// KindSession is a signed-in browser.
	KindSession PrincipalKind = "session"
	// KindAPIKey is a program with a bearer key.
	KindAPIKey PrincipalKind = "api_key"
)

// Principal is the authenticated caller.
type Principal struct {
	Kind    PrincipalKind
	Name    string // key name, or "web UI" for a session
	Session store.Session
}

type principalKey struct{}
type requestKey struct{}

// PrincipalFrom returns the caller stored by the auth middleware.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// requestFrom returns the HTTP request stored by the auth middleware, for
// handlers that need the remote address or the cookies.
func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return r
}

// Source names the caller for records such as overrides.
func (p Principal) Source() string {
	if p.Kind == KindAPIKey {
		return "API key " + p.Name
	}
	return "web UI"
}

// auth holds what the login and the middleware need.
type auth struct {
	store   *store.Store
	secure  bool // set the Secure cookie flag
	limiter *loginLimiter
}

// HashPassword returns an argon2id PHC string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const (
		mem     = 19 * 1024
		iters   = 2
		threads = 1
	)
	key := argon2.IDKey([]byte(password), salt, iters, mem, threads, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", mem, iters, threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a PHC string from HashPassword.
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var mem, iters uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iters, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iters, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// randomToken returns n random bytes as URL-safe base64.
func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashKey returns the stored form of an API key.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// loginLimiter counts failed logins per IP. After maxFailures in the window
// the address must wait.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

const (
	loginWindow  = 5 * time.Minute
	loginMaxFail = 10
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: map[string][]time.Time{}}
}

// allowed reports whether ip can try to log in now.
func (l *loginLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) < loginMaxFail
}

// fail records a failed attempt.
func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip] = append(l.recent(ip), time.Now())
}

// reset clears the count after a good login.
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
}

func (l *loginLimiter) recent(ip string) []time.Time {
	cutoff := time.Now().Add(-loginWindow)
	kept := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, ip)
		return nil
	}
	l.failures[ip] = kept
	return kept
}

// clientIP returns the remote address without the port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// openPaths need no login: the login itself, first-time setup, the health
// detail, the version, and the API description.
var openPaths = map[string]bool{
	"/api/v1/auth/login":   true,
	"/api/v1/auth/setup":   true,
	"/api/v1/auth/session": true,
	"/api/v1/health":       true,
	"/api/v1/system/info":  true,
	"/api/v1/openapi.json": true,
	"/api/v1/openapi.yaml": true,
	"/api/v1/docs":         true,
}

// middleware authenticates every /api request. A session cookie or a
// bearer key sets the principal; a state-changing session request must
// also carry the CSRF header.
func (a *auth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestKey{}, r)
		if openPaths[r.URL.Path] {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		p, err := a.authenticate(r)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "authentication check failed")
			return
		}
		if p == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="jukem"`)
			writeProblem(w, http.StatusUnauthorized, "sign in or send an API key")
			return
		}
		if p.Kind == KindSession && !safeMethod(r.Method) &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(p.Session.CSRFToken)) != 1 {
			writeProblem(w, http.StatusForbidden, "missing or wrong CSRF token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, principalKey{}, *p)))
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// authenticate resolves the caller, or nil when the request carries no
// valid credential.
func (a *auth) authenticate(r *http.Request) (*Principal, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		key := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		k, ok, err := a.store.APIKeyByHash(ctx, hashKey(key))
		if err != nil || !ok {
			return nil, err
		}
		// Recording each use is best effort; a failed update is not a
		// reason to reject the request.
		a.store.TouchAPIKey(ctx, k.ID, clientIP(r))
		return &Principal{Kind: KindAPIKey, Name: k.Name}, nil
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	se, ok, err := a.store.GetSession(ctx, c.Value)
	if err != nil || !ok {
		return nil, err
	}
	if time.Since(se.LastSeen) > time.Hour {
		a.store.TouchSession(ctx, se.ID, time.Now().Add(sessionLife))
	}
	return &Principal{Kind: KindSession, Name: "web UI", Session: se}, nil
}

// startSession creates a session and returns the cookie to set.
func (a *auth) startSession(ctx context.Context, r *http.Request) (store.Session, *http.Cookie, error) {
	se := store.Session{
		ID:        randomToken(32),
		CSRFToken: randomToken(32),
		CreatedAt: time.Now(),
		LastSeen:  time.Now(),
		ExpiresAt: time.Now().Add(sessionLife),
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
	}
	if err := a.store.CreateSession(ctx, se); err != nil {
		return se, nil, err
	}
	return se, a.cookie(se.ID, sessionLife), nil
}

func (a *auth) cookie(value string, life time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secure,
	}
	if life <= 0 {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(life.Seconds())
	}
	return c
}

// errWebSessionOnly rejects an API key on endpoints that manage access.
var errWebSessionOnly = errors.New("this endpoint needs a web session")
