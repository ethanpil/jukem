package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jukem/web"
)

func TestHealthzAndStatic(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("healthz %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP header")
	}

	resp, _ = http.Get(ts.URL + "/")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("index: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("index cache: %q", resp.Header.Get("Cache-Control"))
	}

	// A deep link is the app shell too, for the hash router.
	resp, _ = http.Get(ts.URL + "/library/R.E.M.")
	if resp.StatusCode != 200 {
		t.Fatalf("deep link: %d", resp.StatusCode)
	}

	req, _ := http.NewRequest("GET", ts.URL+"/app/maintenance.js?v=test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, _ = http.DefaultTransport.RoundTrip(req)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("asset: %d enc=%q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset cache: %q", resp.Header.Get("Cache-Control"))
	}

	resp, _ = http.Get(ts.URL + "/api/v1/health")
	if resp.StatusCode != 200 {
		t.Fatalf("health: %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/api/v1/nothing")
	if resp.StatusCode != 401 || resp.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unknown api without login: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestMaintenanceMode(t *testing.T) {
	h, err := NewMaintenance(web.Files, "test", "schema too new", "install a newer package")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/healthz")
	if resp.StatusCode != 503 {
		t.Fatalf("healthz %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/api/v1/health")
	if resp.StatusCode != 200 {
		t.Fatalf("health %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/api/v1/status")
	if resp.StatusCode != 503 {
		t.Fatalf("other api %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/")
	if resp.StatusCode != 503 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("page %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/app/maintenance.js")
	if resp.StatusCode != 200 {
		t.Fatalf("maintenance asset %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/app/anything")
	if resp.StatusCode != 503 {
		t.Fatalf("maintenance mode must not serve the app shell: %d", resp.StatusCode)
	}
}
