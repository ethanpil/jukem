package mpdctl

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderConfig(t *testing.T) {
	c := NewConfig("/var/lib/jukem", "/srv/jukem/music", []Output{{Name: "usb-Device-0", Device: "plughw:CARD=Device,DEV=0"}})
	out := c.Render()
	for _, want := range []string{
		`music_directory        "/srv/jukem/music"`,
		`bind_to_address        "/var/lib/jukem/mpd/mpd.sock"`,
		`restore_paused         "yes"`,
		`auto_update            "no"`,
		`max_playlist_length    "20000"`,
		`name        "usb-Device-0"`,
		`device      "plughw:CARD=Device,DEV=0"`,
		`mixer_type  "software"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "null") || strings.Contains(out, "log_file") {
		t.Errorf("unexpected null output or log_file:\n%s", out)
	}
}

func TestRenderNoOutputsUsesNull(t *testing.T) {
	out := NewConfig("/d", "/m", nil).Render()
	if !strings.Contains(out, `type        "null"`) {
		t.Errorf("expected a null output:\n%s", out)
	}
}

func TestQuoteEscapes(t *testing.T) {
	if got := quote(`a"b\c`); got != `"a\"b\\c"` {
		t.Fatalf("got %s", got)
	}
}

func TestPIDFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, "mpd", slog.New(slog.DiscardHandler), nil)
	os.MkdirAll(s.paths.Dir, 0o750)
	s.writePIDFile(4242)
	rec, err := readPIDFile(filepath.Join(dir, "mpd", "mpd.pid"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.PID != 4242 || rec.ConfFile != s.paths.ConfFile {
		t.Fatalf("got %+v", rec)
	}
	if _, err := readPIDFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error")
	}
}

func TestLineLogger(t *testing.T) {
	var lines []string
	h := &captureHandler{lines: &lines}
	l := &lineLogger{log: slog.New(h)}
	l.Write([]byte("first line\nsec"))
	l.Write([]byte("ond line\n"))
	if len(lines) != 2 || lines[0] != "mpd: first line" || lines[1] != "mpd: second line" {
		t.Fatalf("got %q", lines)
	}
}

func TestSupervisorMissingBinaryReportsError(t *testing.T) {
	dir := t.TempDir()
	events := make(chan Event, 8)
	s := New(dir, filepath.Join(dir, "no-such-mpd"), slog.New(slog.DiscardHandler), func(e Event) { events <- e })
	if err := s.Start(t.Context(), NewConfig(dir, dir, nil)); err != nil {
		t.Fatal(err)
	}
	ev := <-events
	if ev.Kind != EventExited || ev.Err == nil {
		t.Fatalf("got %+v", ev)
	}
	if st := s.Status(); st.Running || st.LastError == "" {
		t.Fatalf("status %+v", st)
	}
	s.Stop()
	if _, err := os.Stat(s.paths.ConfFile); err != nil {
		t.Fatal("config not written")
	}
}
