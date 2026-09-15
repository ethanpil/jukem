package library

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jukem/internal/events"
)

func newTestFiles(t *testing.T) (*Files, string) {
	t.Helper()
	root := t.TempDir()
	lim := Limits{MaxBytes: 1000, Reserve: 0, Extension: func(name string) bool { return strings.HasSuffix(strings.ToLower(name), ".mp3") }}
	f := NewFiles(func() string { return root }, func() Limits { return lim }, nil, events.New(), slog.New(slog.DiscardHandler), "host")
	return f, root
}

func TestUploadStoresFile(t *testing.T) {
	f, root := newTestFiles(t)
	ForgetStat()
	res, err := f.Upload("Rock/song.mp3", "skip", bytes.NewReader([]byte("abc")), 3)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != 3 || res.Skipped {
		t.Fatalf("got %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(root, "Rock", "song.mp3"))
	if err != nil || string(data) != "abc" {
		t.Fatalf("stored %q %v", data, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, ".jukem-tmp")); len(entries) != 0 {
		t.Fatal("temporary file left behind")
	}
	// The pending folder is recorded for the scan, and the scan timer is
	// armed.
	if len(f.pending) != 1 || f.pending[0] != "Rock" || f.timer == nil {
		t.Fatalf("pending %v", f.pending)
	}
	f.timer.Stop()
}

func TestUploadConflictPolicy(t *testing.T) {
	f, root := newTestFiles(t)
	ForgetStat()
	os.MkdirAll(filepath.Join(root, "a"), 0o750)
	os.WriteFile(filepath.Join(root, "a", "x.mp3"), []byte("old"), 0o644)
	res, err := f.Upload("a/x.mp3", "skip", bytes.NewReader([]byte("new")), 3)
	if err != nil || !res.Skipped {
		t.Fatalf("skip: %+v %v", res, err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a", "x.mp3")); string(data) != "old" {
		t.Fatal("skip overwrote the file")
	}
	res, err = f.Upload("a/x.mp3", "replace", bytes.NewReader([]byte("new")), 3)
	if err != nil || res.Skipped {
		t.Fatalf("replace: %+v %v", res, err)
	}
	// The stored file armed the scan timer, and the test has no player.
	f.timer.Stop()
	if data, _ := os.ReadFile(filepath.Join(root, "a", "x.mp3")); string(data) != "new" {
		t.Fatal("replace kept the old file")
	}
}

func TestUploadRejections(t *testing.T) {
	f, _ := newTestFiles(t)
	ForgetStat()
	cases := []struct {
		rel    string
		size   int64
		body   string
		status int
	}{
		{"../x.mp3", 1, "a", http.StatusUnprocessableEntity},
		{"x.exe", 1, "a", http.StatusUnsupportedMediaType},
		{"x.mp3", 5000, "a", http.StatusRequestEntityTooLarge},
		{"big.mp3", -1, strings.Repeat("x", 1001), http.StatusRequestEntityTooLarge},
		{".hidden/x.mp3", 1, "a", http.StatusUnprocessableEntity},
		{"a", 1, "a", http.StatusUnsupportedMediaType},
	}
	os.MkdirAll(filepath.Join(f.root(), "dir.mp3"), 0o750)
	cases = append(cases, struct {
		rel    string
		size   int64
		body   string
		status int
	}{"dir.mp3", 1, "a", http.StatusConflict})
	for _, c := range cases {
		_, err := f.Upload(c.rel, "skip", strings.NewReader(c.body), c.size)
		var oe *OpError
		if !errors.As(err, &oe) || oe.Status != c.status {
			t.Errorf("%s: got %v, want status %d", c.rel, err, c.status)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(f.root(), ".jukem-tmp")); len(entries) != 0 {
		t.Fatal("a rejected upload left a temporary file")
	}
	// A rejected body leaves no empty folder behind.
	f.Upload("New/Album/big.mp3", "skip", strings.NewReader(strings.Repeat("x", 1001)), -1)
	if _, err := os.Stat(filepath.Join(f.root(), "New")); err == nil {
		t.Fatal("rejected upload created its folders")
	}
	for _, i := range []int{0} {
		_ = i
		if f.active != 0 || f.inflight != 0 {
			t.Fatalf("counters not balanced: active=%d inflight=%d", f.active, f.inflight)
		}
	}
}

func TestScanTracking(t *testing.T) {
	f, _ := newTestFiles(t)
	hub := f.events
	ch, unsub := hub.Subscribe()
	defer unsub()
	f.mu.Lock()
	f.scanning, f.scanDir, f.scanAt = true, "Rock", time.Now()
	f.mu.Unlock()
	if !f.ScanPending() {
		t.Fatal("scan should be pending")
	}
	f.NoteUpdate(true)
	if !f.ScanPending() {
		t.Fatal("a running update must not end the scan")
	}
	f.NoteUpdate(false)
	if f.ScanPending() {
		t.Fatal("scan should be over")
	}
	select {
	case ev := <-ch:
		if ev.Type != events.Upload || ev.Ref != "Rock" {
			t.Fatalf("got %+v", ev)
		}
	default:
		t.Fatal("no upload event")
	}
	// A second end event without a scan publishes nothing.
	f.NoteUpdate(false)
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	default:
	}
}

func TestCheck(t *testing.T) {
	f, root := newTestFiles(t)
	ForgetStat()
	os.WriteFile(filepath.Join(root, "have.mp3"), []byte("x"), 0o644)
	res := f.Check([]string{"have.mp3", "new.mp3", "bad.exe", "../x.mp3", ""})
	if len(res.Existing) != 1 || res.Existing[0] != "have.mp3" {
		t.Fatalf("existing %v", res.Existing)
	}
	if len(res.Unsupported) != 1 || len(res.Invalid) != 2 || res.MaxBytes != 1000 {
		t.Fatalf("got %+v", res)
	}
}

func TestCommonFolder(t *testing.T) {
	cases := map[string][]string{
		"Christmas": {"Christmas/2026", "Christmas/2025/Jazz", "Christmas"},
		"":          {"Rock", "Jazz"},
		"a/b":       {"a/b", "a/b/c"},
		"x":         {"x"},
	}
	for want, in := range cases {
		if got := commonFolder(in); got != want {
			t.Errorf("commonFolder(%v) = %q, want %q", in, got, want)
		}
	}
	if commonFolder([]string{"a", "."}) != "" {
		t.Fatal("root upload must scan everything")
	}
}

func TestNewFolderAndPermissions(t *testing.T) {
	f, root := newTestFiles(t)
	if err := f.NewFolder("New/Deep"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(root, "New", "Deep")); err != nil || !st.IsDir() {
		t.Fatal("folder not created")
	}
	var oe *OpError
	if err := f.NewFolder("New/Deep"); !errors.As(err, &oe) || oe.Status != http.StatusConflict {
		t.Fatalf("duplicate: %v", err)
	}
	if err := f.NewFolder(""); !errors.As(err, &oe) || oe.Status != http.StatusUnprocessableEntity {
		t.Fatalf("root: %v", err)
	}
	rep, err := f.CheckPermissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 3 || len(rep.Problems) != 0 {
		t.Fatalf("got %+v", rep)
	}
	fix, cmd := f.fixText(root)
	if !strings.Contains(cmd, "chown -R jukem:jukem") || fix == "" {
		t.Fatal(fix, cmd)
	}
	f.runtime = "docker"
	if fix, cmd := f.fixText(root); !strings.Contains(fix, "Docker host") || !strings.Contains(cmd, "1000:1000") || !strings.Contains(cmd, root) {
		t.Fatal(fix, cmd)
	}
}

func TestWriteTempLimit(t *testing.T) {
	dir := t.TempDir()
	tmp, n, err := writeTemp(dir, io.LimitReader(strings.NewReader("hello"), 5), 5)
	if err != nil || n != 5 {
		t.Fatal(n, err)
	}
	os.Remove(tmp)
	if _, _, err := writeTemp(dir, strings.NewReader("hello!"), 5); err == nil {
		t.Fatal("expected the limit error")
	}
}
