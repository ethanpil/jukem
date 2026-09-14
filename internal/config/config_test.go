package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	t.Setenv("JUKEM_LISTEN", "")
	os.Unsetenv("JUKEM_LISTEN")
	os.Unsetenv("JUKEM_DATA_DIR")
	os.Unsetenv("JUKEM_LOG_FILE")
	cfg, warnings, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Fatalf("got %+v", cfg)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected a warning, got %v", warnings)
	}
}

func TestLoadFileAndUnknownKeys(t *testing.T) {
	os.Unsetenv("JUKEM_LISTEN")
	os.Unsetenv("JUKEM_DATA_DIR")
	os.Unsetenv("JUKEM_LOG_FILE")
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("listen: \":8080\"\nbogus: 1\nlog_file: \"\"\n"), 0o600)
	cfg, warnings, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8080" || cfg.DataDir != "/var/lib/jukem" || cfg.LogFile != "" {
		t.Fatalf("got %+v", cfg)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bogus") {
		t.Fatalf("got warnings %v", warnings)
	}
}

func TestEnvOverrides(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("listen: \":80\"\nlog_file: /var/log/x\n"), 0o600)
	t.Setenv("JUKEM_LISTEN", ":9000")
	t.Setenv("JUKEM_LOG_FILE", "")
	t.Setenv("JUKEM_DATA_DIR", "/tmp/d")
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9000" || cfg.LogFile != "" || cfg.DataDir != "/tmp/d" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestBadYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("listen: [\n"), 0o600)
	if _, _, err := Load(p); err == nil {
		t.Fatal("expected error")
	}
}

func TestEnvAppliesWhenFileIsBroken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("listen: [\n"), 0o600)
	t.Setenv("JUKEM_LISTEN", ":8080")
	cfg, _, err := Load(p)
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ParseError, got %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Fatalf("env override lost: %+v", cfg)
	}
}

func TestNullKeepsDefault(t *testing.T) {
	os.Unsetenv("JUKEM_LOG_FILE")
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("log_file:\n"), 0o600)
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFile != Default().LogFile {
		t.Fatalf("got %q", cfg.LogFile)
	}
}
