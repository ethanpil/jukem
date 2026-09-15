package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"jukem/internal/store"
	"jukem/internal/watchdog"
	"jukem/web"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "jukem.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := New(Options{Version: "test", Static: web.Files, Store: db, Health: func() watchdog.Report {
		return watchdog.Report{Status: watchdog.StatusOK}
	}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// client is a small test client that keeps the cookie and CSRF token.
type client struct {
	t    *testing.T
	base string
	cook string
	csrf string
	auth string
}

func (c *client) do(method, path string, body any) (*http.Response, map[string]any) {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, c.base+path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cook != "" {
		req.Header.Set("Cookie", c.cook)
	}
	if c.csrf != "" {
		req.Header.Set(csrfHeader, c.csrf)
	}
	if c.auth != "" {
		req.Header.Set("Authorization", "Bearer "+c.auth)
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if sc := resp.Header.Get("Set-Cookie"); sc != "" {
		c.cook = strings.SplitN(sc, ";", 2)[0]
	}
	if tok, ok := out["csrf_token"].(string); ok && tok != "" {
		c.csrf = tok
	}
	return resp, out
}

func TestSetupLoginCSRFAndAPIKeys(t *testing.T) {
	ts := newTestServer(t)
	c := &client{t: t, base: ts.URL}

	resp, out := c.do("GET", "/api/v1/auth/session", nil)
	if resp.StatusCode != 200 || out["setup_required"] != true {
		t.Fatalf("session before setup: %d %v", resp.StatusCode, out)
	}
	if resp, _ := c.do("GET", "/api/v1/api-keys", nil); resp.StatusCode != 401 {
		t.Fatalf("protected endpoint before login: %d", resp.StatusCode)
	}
	if resp, _ := c.do("POST", "/api/v1/auth/setup", map[string]string{"password": "short"}); resp.StatusCode != 422 {
		t.Fatalf("short password: %d", resp.StatusCode)
	}
	resp, out = c.do("POST", "/api/v1/auth/setup", map[string]string{"password": "correct horse"})
	if resp.StatusCode != 201 || c.cook == "" || c.csrf == "" {
		t.Fatalf("setup: %d %v cookie=%q", resp.StatusCode, out, c.cook)
	}
	if resp, _ := c.do("POST", "/api/v1/auth/setup", map[string]string{"password": "correct horse"}); resp.StatusCode != 409 {
		t.Fatalf("second setup: %d", resp.StatusCode)
	}

	// A state change without the CSRF header is refused, with it accepted.
	saved := c.csrf
	c.csrf = ""
	resp, _ = c.do("POST", "/api/v1/api-keys", map[string]string{"name": "tablet"})
	if resp.StatusCode != 403 {
		t.Fatalf("missing csrf: %d", resp.StatusCode)
	}
	c.csrf = saved
	resp, out = c.do("POST", "/api/v1/api-keys", map[string]string{"name": "tablet"})
	if resp.StatusCode != 201 {
		t.Fatalf("create key: %d %v", resp.StatusCode, out)
	}
	key, _ := out["key"].(string)
	id := out["id"].(float64)

	// The key works for normal endpoints and not for key management.
	kc := &client{t: t, base: ts.URL, auth: key}
	if resp, _ := kc.do("GET", "/api/v1/auth/session", nil); resp.StatusCode != 200 {
		t.Fatalf("key session: %d", resp.StatusCode)
	}
	if resp, _ := kc.do("GET", "/api/v1/api-keys", nil); resp.StatusCode != 403 {
		t.Fatalf("key on key management: %d", resp.StatusCode)
	}
	if resp, _ := (&client{t: t, base: ts.URL, auth: "nope"}).do("GET", "/api/v1/system/info", nil); resp.StatusCode != 200 {
		t.Fatalf("open endpoint: %d", resp.StatusCode)
	}

	// Revoke: the next request with the key fails.
	if resp, _ := c.do("DELETE", "/api/v1/api-keys/"+strconv.Itoa(int(id)), nil); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, _ := kc.do("GET", "/api/v1/api-keys", nil); resp.StatusCode != 401 {
		t.Fatalf("revoked key: %d", resp.StatusCode)
	}

	// Logout ends the session; login with the wrong password fails and the
	// right one works.
	if resp, _ := c.do("POST", "/api/v1/auth/logout", nil); resp.StatusCode != 204 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	c.cook, c.csrf = "", ""
	if resp, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "wrong password"}); resp.StatusCode != 401 {
		t.Fatalf("bad login: %d", resp.StatusCode)
	}
	if resp, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "correct horse"}); resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	// Changing the password signs every session out.
	resp, _ = c.do("PUT", "/api/v1/auth/password", map[string]string{"current": "correct horse", "password": "new password 1"})
	if resp.StatusCode != 204 {
		t.Fatalf("change password: %d", resp.StatusCode)
	}
	if resp, _ := c.do("GET", "/api/v1/api-keys", nil); resp.StatusCode != 401 {
		t.Fatalf("session after password change: %d", resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ts := newTestServer(t)
	c := &client{t: t, base: ts.URL}
	c.do("POST", "/api/v1/auth/setup", map[string]string{"password": "correct horse"})
	c.cook, c.csrf = "", ""
	for i := 0; i < loginMaxFail; i++ {
		c.do("POST", "/api/v1/auth/login", map[string]string{"password": "wrong password"})
	}
	if resp, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "correct horse"}); resp.StatusCode != 429 {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
}

func TestCookieFlags(t *testing.T) {
	a := &auth{secure: true}
	c := a.sessionCookie("abc")
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("cookie %+v", c)
	}
	if cc := a.clearCookie(); cc.MaxAge != -1 || cc.Value != "" {
		t.Fatalf("clear cookie %+v", cc)
	}
	a.secure = false
	if a.sessionCookie("abc").Secure {
		t.Fatal("Secure set without TLS")
	}
}

func TestExpiredAPIKeyIsRejected(t *testing.T) {
	ts := newTestServer(t)
	c := &client{t: t, base: ts.URL}
	c.do("POST", "/api/v1/auth/setup", map[string]string{"password": "correct horse"})
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	resp, out := c.do("POST", "/api/v1/api-keys", map[string]any{"name": "old", "expires_at": past})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	kc := &client{t: t, base: ts.URL, auth: out["key"].(string)}
	if resp, _ := kc.do("GET", "/api/v1/auth/session", nil); resp.StatusCode != 200 {
		t.Fatalf("open path with expired key: %d", resp.StatusCode)
	}
	if resp, _ := kc.do("GET", "/api/v1/nothing", nil); resp.StatusCode != 401 {
		t.Fatalf("expired key must fail: %d", resp.StatusCode)
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("secret value")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") || !VerifyPassword(h, "secret value") || VerifyPassword(h, "other") || VerifyPassword("garbage", "x") {
		t.Fatal("hash round trip")
	}
}

func TestOpenAPIServed(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/openapi.json")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("openapi: %v %d", err, resp.StatusCode)
	}
}
