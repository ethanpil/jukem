package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanRel(t *testing.T) {
	good := map[string]string{
		"":                       "",
		"/":                      "",
		"Rock/Band/01 Song.flac": "Rock/Band/01 Song.flac",
		"Christmas/2026/":        "Christmas/2026",
		"a\\b":                   "a/b",
		"ünïcödé/naïve.mp3":      "ünïcödé/naïve.mp3",
	}
	for in, want := range good {
		got, err := CleanRel(in)
		if err != nil || got != want {
			t.Errorf("CleanRel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"../x", "a/../b", "a/./b", "a//b", ".hidden/x.mp3", "a/.jukem-tmp/x", "/etc/passwd", "/Christmas",
		"a\x00b", "a\nb", strings.Repeat("x", 256) + ".mp3", ".", "..",
	}
	for _, in := range bad {
		if _, err := CleanRel(in); err == nil {
			t.Errorf("CleanRel(%q) accepted", in)
		}
	}
}

func TestAbsStaysInsideRoot(t *testing.T) {
	root := filepath.Join("srv", "music")
	abs, err := Abs(root, "a/b.mp3")
	if err != nil || abs != filepath.Join(root, "a", "b.mp3") {
		t.Fatalf("got %q %v", abs, err)
	}
	if abs, err := Abs(root, ""); err != nil || abs != filepath.Clean(root) {
		t.Fatalf("root: %q %v", abs, err)
	}
	if _, err := Abs(root, "../x"); err == nil {
		t.Fatal("escaped root")
	}
	// A root at the top of the file system works too.
	top := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	if abs, err := Abs(top, "a/b.mp3"); err != nil || abs != filepath.Join(top, "a", "b.mp3") {
		t.Fatalf("top root: %q %v", abs, err)
	}
}

func TestResolveRejectsSymlinkOutOfRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "music")
	outside := filepath.Join(base, "secret.db")
	os.MkdirAll(filepath.Join(root, "a"), 0o750)
	os.WriteFile(filepath.Join(root, "a", "song.mp3"), []byte("x"), 0o600)
	os.WriteFile(outside, []byte("x"), 0o600)
	if err := os.Symlink(outside, filepath.Join(root, "a", "link.mp3")); err != nil {
		t.Skip("symlinks not available:", err)
	}
	if _, err := Resolve(root, "a/song.mp3"); err != nil {
		t.Fatalf("real file: %v", err)
	}
	if _, err := Resolve(root, "a/link.mp3"); err == nil {
		t.Fatal("symlink out of the root was accepted")
	}
	if _, err := Resolve(root, "a/missing.mp3"); !os.IsNotExist(err) {
		t.Fatalf("missing file: %v", err)
	}
}
