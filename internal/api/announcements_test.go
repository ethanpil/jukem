package api

import (
	"strconv"
	"testing"
)

// signIn creates the first password and returns a client that carries the
// session and the CSRF token.
func signIn(t *testing.T, base string) *client {
	t.Helper()
	c := &client{t: t, base: base}
	if resp, out := c.do("POST", "/api/v1/auth/setup", map[string]string{"password": "correct horse"}); resp.StatusCode != 201 {
		t.Fatalf("setup: %d %v", resp.StatusCode, out)
	}
	return c
}

func TestAnnouncementsCRUD(t *testing.T) {
	ts := newTestServer(t)
	c := signIn(t, ts.URL)

	resp, out := c.do("GET", "/api/v1/announcements", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("empty list: %d %v", resp.StatusCode, out)
	}
	if list, _ := out["announcements"].([]any); len(list) != 0 {
		t.Fatalf("a new database has no announcements: %v", out)
	}

	create := map[string]any{
		"name": "Closing time", "enabled": true, "days": 31, "mode": "at", "at_time": "20:45",
		"source_kind": "file", "source_ref": "announcements/closing.mp3",
	}
	resp, out = c.do("POST", "/api/v1/announcements", create)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	id := int(out["id"].(float64))
	if id == 0 || out["name"] != "Closing time" || out["at_time"] != "20:45" {
		t.Fatalf("created announcement: %v", out)
	}

	// An hourly announcement keeps its window and drops the single time.
	every := map[string]any{
		"name": "Offer of the day", "enabled": true, "days": 127, "mode": "every",
		"start_time": "09:00", "end_time": "17:00", "every_minutes": 45,
		"source_kind": "cycle", "source_ref": "announcements/offers/",
	}
	resp, out = c.do("POST", "/api/v1/announcements", every)
	if resp.StatusCode != 201 {
		t.Fatalf("create every: %d %v", resp.StatusCode, out)
	}
	if out["source_ref"] != "announcements/offers" {
		t.Fatalf("the source path is cleaned: %v", out["source_ref"])
	}
	if _, ok := out["at_time"]; ok {
		t.Fatalf("mode every keeps no single time: %v", out)
	}

	resp, out = c.do("GET", "/api/v1/announcements", nil)
	if list, _ := out["announcements"].([]any); resp.StatusCode != 200 || len(list) != 2 {
		t.Fatalf("list: %d %v", resp.StatusCode, out)
	}

	update := map[string]any{
		"name": "Closing time", "enabled": false, "days": 31, "mode": "at", "at_time": "21:00",
		"source_kind": "file", "source_ref": "announcements/closing.mp3",
	}
	resp, out = c.do("PUT", "/api/v1/announcements/"+strconv.Itoa(id), update)
	if resp.StatusCode != 200 || out["at_time"] != "21:00" || out["enabled"] != false {
		t.Fatalf("update: %d %v", resp.StatusCode, out)
	}

	if resp, _ := c.do("PUT", "/api/v1/announcements/9999", update); resp.StatusCode != 404 {
		t.Fatalf("update of an announcement that is gone: %d", resp.StatusCode)
	}
	if resp, _ := c.do("DELETE", "/api/v1/announcements/"+strconv.Itoa(id), nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, _ := c.do("DELETE", "/api/v1/announcements/"+strconv.Itoa(id), nil); resp.StatusCode != 404 {
		t.Fatalf("second delete: %d", resp.StatusCode)
	}
}

func TestAnnouncementsValidation(t *testing.T) {
	ts := newTestServer(t)
	c := signIn(t, ts.URL)

	for name, body := range map[string]map[string]any{
		"no name":          {"name": "  ", "days": 1, "mode": "at", "at_time": "10:15", "source_kind": "file", "source_ref": "a.mp3"},
		"at without time":  {"name": "a", "days": 1, "mode": "at", "source_kind": "file", "source_ref": "a.mp3"},
		"every without":    {"name": "a", "days": 1, "mode": "every", "start_time": "09:00", "source_kind": "file", "source_ref": "a.mp3"},
		"end before start": {"name": "a", "days": 1, "mode": "every", "start_time": "17:00", "end_time": "09:00", "every_minutes": 30, "source_kind": "file", "source_ref": "a.mp3"},
		"file without ref": {"name": "a", "days": 1, "mode": "at", "at_time": "10:15", "source_kind": "file", "source_ref": ""},
		"path escape":      {"name": "a", "days": 1, "mode": "at", "at_time": "10:15", "source_kind": "file", "source_ref": "../secrets.mp3"},
	} {
		if resp, out := c.do("POST", "/api/v1/announcements", body); resp.StatusCode != 422 {
			t.Errorf("%s: %d %v", name, resp.StatusCode, out)
		}
	}

	// An unknown mode or source kind is refused by the schema itself.
	for name, body := range map[string]map[string]any{
		"bad mode": {"name": "a", "days": 1, "mode": "sometimes", "source_kind": "file", "source_ref": "a.mp3"},
		"bad kind": {"name": "a", "days": 1, "mode": "at", "at_time": "10:15", "source_kind": "magic", "source_ref": "a.mp3"},
		"bad days": {"name": "a", "days": 0, "mode": "at", "at_time": "10:15", "source_kind": "file", "source_ref": "a.mp3"},
		"bad time": {"name": "a", "days": 1, "mode": "at", "at_time": "25:00", "source_kind": "file", "source_ref": "a.mp3"},
	} {
		if resp, out := c.do("POST", "/api/v1/announcements", body); resp.StatusCode != 422 {
			t.Errorf("%s: %d %v", name, resp.StatusCode, out)
		}
	}
}
