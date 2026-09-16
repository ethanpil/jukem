package update

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"jukem/internal/store"
)

func newChecker(t *testing.T, current string, h http.HandlerFunc) (*Checker, *httptest.Server) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "jukem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	c := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), current, func() bool { return true })
	c.url = ts.URL
	return c, ts
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.9", "0.1.8", true},
		{"0.2.0", "0.1.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.8", "0.1.8", false},
		{"0.1.7", "0.1.8", false},
		{"0.1.10", "0.1.9", true},
		{"v0.1.9", "0.1.8", true},
		// A build from source, or a version with a suffix, is never newer.
		{"0.1.9", "dev", false},
		{"dev", "0.1.8", false},
		{"0.2.0-rc1", "0.1.8", false},
		{"0.1", "0.1.8", false},
		{"", "0.1.8", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckFindsANewerRelease(t *testing.T) {
	var gotUA, gotAccept string
	c, _ := newChecker(t, "0.1.8", func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		io.WriteString(w, `{"tag_name":"v0.1.9","html_url":"https://example.test/r/v0.1.9","published_at":"2026-09-16T10:00:00Z"}`)
	})
	ctx := context.Background()
	st, err := c.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "0.1.9" || !st.Available || st.Current != "0.1.8" {
		t.Fatalf("got %+v", st)
	}
	if st.URL != "https://example.test/r/v0.1.9" || st.CheckedAt.IsZero() || st.Error != "" {
		t.Fatalf("got %+v", st)
	}
	if gotUA != "jukem/0.1.8" || gotAccept != "application/vnd.github+json" {
		t.Fatalf("headers: %q %q", gotUA, gotAccept)
	}
	// The result survives a restart, and Available is computed again.
	if again := c.Status(ctx); again.Version != "0.1.9" || !again.Available {
		t.Fatalf("stored status: %+v", again)
	}
}

func TestCheckWithTheSameVersion(t *testing.T) {
	c, _ := newChecker(t, "0.1.9", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"tag_name":"v0.1.9","html_url":"https://example.test/r"}`)
	})
	st, err := c.Check(context.Background())
	if err != nil || st.Available {
		t.Fatalf("got %+v err %v", st, err)
	}
}

func TestDraftAndPrereleaseAreIgnored(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v0.2.0","draft":true}`,
		`{"tag_name":"v0.2.0","prerelease":true}`,
	} {
		c, _ := newChecker(t, "0.1.8", func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, body)
		})
		st, err := c.Check(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if st.Version != "" || st.Available {
			t.Fatalf("%s: got %+v", body, st)
		}
	}
}

func TestFailedCheckKeepsTheLastResult(t *testing.T) {
	fail := false
	c, _ := newChecker(t, "0.1.8", func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(w, "rate limited", http.StatusForbidden)
			return
		}
		io.WriteString(w, `{"tag_name":"v0.1.9","html_url":"https://example.test/r"}`)
	})
	ctx := context.Background()
	if _, err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	fail = true
	st, err := c.Check(ctx)
	if err == nil {
		t.Fatal("want an error")
	}
	if st.Version != "0.1.9" || !st.Available || st.Error == "" {
		t.Fatalf("got %+v", st)
	}
}

func TestBadAnswerIsAnError(t *testing.T) {
	c, _ := newChecker(t, "0.1.8", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "not json")
	})
	st, err := c.Check(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	if st.Error == "" || st.Available {
		t.Fatalf("got %+v", st)
	}
}

func TestStatusBeforeAnyCheck(t *testing.T) {
	c, _ := newChecker(t, "0.1.8", func(http.ResponseWriter, *http.Request) {})
	st := c.Status(context.Background())
	if st.Current != "0.1.8" || st.Version != "" || st.Available || !st.CheckedAt.IsZero() {
		t.Fatalf("got %+v", st)
	}
}

func TestDue(t *testing.T) {
	c, _ := newChecker(t, "0.1.8", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"tag_name":"v0.1.9"}`)
	})
	ctx := context.Background()
	if !c.due(ctx) {
		t.Fatal("the first check is due")
	}
	if _, err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if c.due(ctx) {
		t.Fatal("a check that just ran is not due")
	}
	old := Release{Version: "0.1.9", CheckedAt: time.Now().Add(-Interval - time.Minute)}
	if err := c.store.SetState(ctx, stateKey, old); err != nil {
		t.Fatal(err)
	}
	if !c.due(ctx) {
		t.Fatal("an old check is due")
	}
}
