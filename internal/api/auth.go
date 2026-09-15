package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"jukem/internal/store"
)

const (
	sessionCookieName = "jukem_session"
	csrfHeader        = "X-CSRF-Token"
	sessionLife       = 30 * 24 * time.Hour
	// touchInterval limits how often a request records its use, so a
	// polling client does not write to the database on every request.
	touchInterval = time.Hour
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
	// renew is set when the session was extended, so the cookie is sent
	// again with a new lifetime.
	renew bool
}

// hashSlots bounds the password hashes in flight. One argon2 hash takes
// 19 MiB, and login and setup are open endpoints.
const hashSlots = 2

// acquireHash takes a hash slot, or answers 429 when none is free soon.
func (a *auth) acquireHash() (func(), error) {
	select {
	case a.hashSem <- struct{}{}:
		return func() { <-a.hashSem }, nil
	case <-time.After(2 * time.Second):
		return nil, huma.Error429TooManyRequests("too many password checks at once; try again")
	}
}

type principalKey struct{}
type requestKey struct{}

// PrincipalFrom returns the caller stored by the auth middleware.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// requestFrom returns the HTTP request stored by the auth middleware, for
// handlers that need the client address, the scheme or the user agent.
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
	limiter *loginLimiter
	hashSem chan struct{}
	proxies []netip.Prefix // trusted reverse proxies
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

// loginLimiter counts failed logins per IP. After loginMaxFail failures in
// the window the address must wait.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

const (
	loginWindow  = 5 * time.Minute
	loginMaxFail = 10
	// limiterSweepAt bounds the map: past this many addresses, a failure
	// first drops every address with no recent failures.
	limiterSweepAt = 1000
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
	if len(l.failures) >= limiterSweepAt {
		for other := range l.failures {
			l.recent(other)
		}
	}
	l.failures[ip] = append(l.recent(ip), time.Now())
}

// reset clears the count after a good login.
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
}

// recent drops the failures outside the window and returns the rest.
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

// clientIP returns the address of the client. A request from a trusted
// reverse proxy gives the client in X-Forwarded-For. jukem reads that list
// from the right and takes the first address that is not a trusted proxy.
// A client cannot use a false header to hide from the login limit, because
// jukem reads the header only from a trusted proxy.
func (a *auth) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !a.fromProxy(r) {
		return host
	}
	parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			break
		}
		if !a.trusted(ip) {
			return ip.Unmap().String()
		}
	}
	return host
}

// fromProxy reports whether the request comes from a trusted proxy.
func (a *auth) fromProxy(r *http.Request) bool {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	return err == nil && a.trusted(ap.Addr())
}

func (a *auth) trusted(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range a.proxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// openPaths are reachable without a credential. The same endpoints carry
// an empty Security list in the OpenAPI document.
var openPaths = map[string]bool{
	apiPrefix + "/auth/login":       true,
	apiPrefix + "/auth/setup":       true,
	apiPrefix + "/auth/session":     true,
	apiPrefix + "/health":           true,
	apiPrefix + "/system/info":      true,
	apiPrefix + "/openapi.json":     true,
	apiPrefix + "/openapi.yaml":     true,
	apiPrefix + "/openapi-3.0.json": true,
	apiPrefix + "/openapi-3.0.yaml": true,
	apiPrefix + "/docs":             true,
	apiPrefix + "/schemas/":         true,
}

func isOpen(path string) bool {
	if openPaths[path] {
		return true
	}
	return strings.HasPrefix(path, apiPrefix+"/schemas/")
}

// middleware authenticates every /api request and stores the principal.
// An open path passes with or without a credential; every other path needs
// one, and a state-changing session request must also carry the CSRF
// header.
func (a *auth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestKey{}, r)
		p, err := a.authenticate(r)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "authentication check failed")
			return
		}
		if p != nil {
			ctx = context.WithValue(ctx, principalKey{}, *p)
			if p.renew {
				http.SetCookie(w, a.sessionCookie(r, p.Session.ID))
			}
		}
		if isOpen(r.URL.Path) {
			next.ServeHTTP(w, r.WithContext(ctx))
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
		next.ServeHTTP(w, r.WithContext(ctx))
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
		if k.LastUsedAt == nil || time.Since(*k.LastUsedAt) > touchInterval {
			// A failed update does not reject the request.
			a.store.TouchAPIKey(ctx, k.ID, a.clientIP(r))
		}
		return &Principal{Kind: KindAPIKey, Name: k.Name}, nil
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	se, ok, err := a.store.GetSession(ctx, c.Value)
	if err != nil || !ok {
		return nil, err
	}
	renew := time.Since(se.LastSeen) > touchInterval
	if renew {
		a.store.TouchSession(ctx, se.ID, time.Now().Add(sessionLife))
	}
	return &Principal{Kind: KindSession, Name: "web UI", Session: se, renew: renew}, nil
}

// startSession creates a session and returns the cookie to set.
func (a *auth) startSession(ctx context.Context, r *http.Request) (store.Session, *http.Cookie, error) {
	se := store.Session{
		ID:        randomToken(32),
		CSRFToken: randomToken(32),
		CreatedAt: time.Now(),
		LastSeen:  time.Now(),
		ExpiresAt: time.Now().Add(sessionLife),
		IP:        a.clientIP(r),
		UserAgent: r.UserAgent(),
	}
	if err := a.store.CreateSession(ctx, se); err != nil {
		return se, nil, err
	}
	return se, a.sessionCookie(r, se.ID), nil
}

// sessionCookie builds the session cookie. The cookie is Secure when a
// trusted reverse proxy reports that the browser used HTTPS.
func (a *auth) sessionCookie(r *http.Request, id string) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		MaxAge:   int(sessionLife.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.fromProxy(r) && forwardedProto(r) == "https",
	}
}

// forwardedProto returns the scheme the browser used, from the first
// proxy in the chain. It reads X-Forwarded-Proto, then the proto parameter
// of the standard Forwarded header.
func forwardedProto(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		first, _, _ := strings.Cut(v, ",")
		return strings.ToLower(strings.TrimSpace(first))
	}
	first, _, _ := strings.Cut(r.Header.Get("Forwarded"), ",")
	for _, pair := range strings.Split(first, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && strings.EqualFold(k, "proto") {
			return strings.ToLower(strings.Trim(v, `"`))
		}
	}
	return ""
}

// clearCookie tells the browser to drop the session cookie.
func (a *auth) clearCookie(r *http.Request) *http.Cookie {
	c := a.sessionCookie(r, "")
	c.MaxAge = -1
	return c
}
