package mpdctl

import (
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
	if strings.Contains(out, "null") {
		t.Error("null output present although a device exists")
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
	s := New(dir, "mpd", nil, nil)
	os.MkdirAll(s.paths.Dir, 0o750)
	s.log = testLogger()
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

func TestParseInt(t *testing.T) {
	if n, err := parseInt("12"); err != nil || n != 12 {
		t.Fatal(n, err)
	}
	if _, err := parseInt("x1"); err == nil {
		t.Fatal("expected error")
	}
}
