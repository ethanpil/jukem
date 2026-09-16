package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"jukem/internal/events"
	"jukem/internal/mpdctl"
	"jukem/internal/player"
	"jukem/internal/store"
	"jukem/internal/update"
	"jukem/internal/watchdog"
	"jukem/web"
)

// newUpdateServer builds a server whose update functions answer with the
// given status and error, so the tests do not reach GitHub.
func newUpdateServer(t *testing.T, st update.Result, checkErr error) *httptest.Server {
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
	ev := events.New()
	log := slog.New(slog.DiscardHandler)
	// The player is here although these tests do not use it: it puts
	// player.Status in the OpenAPI registry beside the answer of the
	// update endpoints. Two registered types with one name stop the
	// server at the start, and this server must find that.
	pl := player.New(mpdctl.NewPool(filepath.Join(dir, "mpd.sock"), 1), func() (int, int) { return 0, 100 }, 0, func(int64) {})
	s, err := New(Options{
		Version: "0.1.8", Static: web.Files, Store: db, Player: pl, Events: ev,
		Health:       func() watchdog.Report { return watchdog.Report{Status: watchdog.StatusOK} },
		Alerter:      watchdog.NewAlerter(db, ev, log, func() watchdog.Webhook { return watchdog.Webhook{} }),
		Owner:        func() player.Owner { return player.Owner{} },
		UpdateStatus: func(context.Context) update.Result { return st },
		CheckUpdate:  func(context.Context) (update.Result, error) { return st, checkErr },
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestUpdateStatusAndCheck(t *testing.T) {
	st := update.Result{Current: "0.1.8", Available: true}
	st.Version, st.URL = "0.1.9", "https://example.test/r/v0.1.9"
	ts := newUpdateServer(t, st, nil)
	c := firstUser(t, ts.URL)

	resp, out := c.do("GET", "/api/v1/system/update", nil)
	if resp.StatusCode != 200 || out["current"] != "0.1.8" || out["version"] != "0.1.9" || out["available"] != true {
		t.Fatalf("status: %d %v", resp.StatusCode, out)
	}
	resp, out = c.do("POST", "/api/v1/system/update/check", nil)
	if resp.StatusCode != 200 || out["version"] != "0.1.9" {
		t.Fatalf("check: %d %v", resp.StatusCode, out)
	}
}

// A check that cannot reach GitHub is a fault of the other side.
func TestUpdateCheckThatFails(t *testing.T) {
	ts := newUpdateServer(t, update.Result{Current: "0.1.8"}, errors.New("cannot reach GitHub"))
	c := firstUser(t, ts.URL)
	if resp, out := c.do("POST", "/api/v1/system/update/check", nil); resp.StatusCode != 502 {
		t.Fatalf("check: %d %v", resp.StatusCode, out)
	}
}

// The check is behind the login, and it needs a web session: an API key
// must not make the appliance send requests to the outside.
func TestUpdateNeedsAuth(t *testing.T) {
	ts := newUpdateServer(t, update.Result{Current: "0.1.8"}, nil)
	anon := &client{t: t, base: ts.URL}
	if resp, _ := anon.do("GET", "/api/v1/system/update", nil); resp.StatusCode != 401 {
		t.Fatalf("status without a session: %d", resp.StatusCode)
	}
	if resp, _ := anon.do("POST", "/api/v1/system/update/check", nil); resp.StatusCode != 401 {
		t.Fatalf("check without a session: %d", resp.StatusCode)
	}

	c := firstUser(t, ts.URL)
	_, out := c.do("POST", "/api/v1/api-keys", map[string]string{"name": "tablet"})
	key, _ := out["key"].(string)
	api := &client{t: t, base: ts.URL, auth: key}
	if resp, _ := api.do("GET", "/api/v1/system/update", nil); resp.StatusCode != 200 {
		t.Fatalf("status with an API key: %d", resp.StatusCode)
	}
	if resp, _ := api.do("POST", "/api/v1/system/update/check", nil); resp.StatusCode != 403 {
		t.Fatalf("check with an API key: %d", resp.StatusCode)
	}
}
