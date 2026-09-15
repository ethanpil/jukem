// Package config loads the bootstrap settings. The database holds every other
// setting, and the web UI changes them.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the bootstrap settings read from /etc/jukem/config.yaml.
type Config struct {
	Listen  string `yaml:"listen"`
	DataDir string `yaml:"data_dir"`
	LogFile string `yaml:"log_file"`
	// TrustedProxies are the addresses of reverse proxies. jukem reads
	// X-Forwarded-For and X-Forwarded-Proto only from them.
	TrustedProxies []netip.Prefix `yaml:"-"`
}

// ParseError reports a config file that does not parse. The caller shows a
// different fix for this than for a file it cannot read.
type ParseError struct {
	Path string
	Err  error
}

func (e *ParseError) Error() string { return fmt.Sprintf("parse %s: %v", e.Path, e.Err) }
func (e *ParseError) Unwrap() error { return e.Err }

// Default returns the settings that apply when a key is missing.
func Default() Config {
	return Config{
		Listen:         ":80",
		DataDir:        "/var/lib/jukem",
		LogFile:        "/var/log/jukem/jukem.log",
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")},
	}
}

// Load reads the file at path and applies the environment overrides. It
// returns a warning for each unknown key. A missing file is not an error: the
// defaults and the environment apply. The environment applies also when the
// file fails, so that maintenance mode listens where the operator expects.
func Load(path string) (Config, []string, error) {
	cfg := Default()
	var warnings []string
	var loadErr error

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		w, err := parse(data, &cfg)
		if err != nil {
			loadErr = &ParseError{Path: path, Err: err}
		}
		warnings = append(warnings, w...)
	case errors.Is(err, os.ErrNotExist):
		warnings = append(warnings, fmt.Sprintf("config file %s not found, using defaults", path))
	default:
		loadErr = fmt.Errorf("read %s: %w", path, err)
	}

	warnings = append(warnings, applyEnv(&cfg)...)
	// MPD reads the socket path from the data dir, so it must be absolute.
	if !filepath.IsAbs(cfg.DataDir) && !strings.HasPrefix(cfg.DataDir, "/") {
		if abs, err := filepath.Abs(cfg.DataDir); err == nil {
			cfg.DataDir = abs
		}
	}
	return cfg, warnings, loadErr
}

// parse decodes YAML into cfg with one pass and reports unknown keys. A key
// that is present with a null value keeps its default.
func parse(data []byte, cfg *Config) ([]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("top level must be a mapping")
	}
	known := map[string]bool{"listen": true, "data_dir": true, "log_file": true, "trusted_proxies": true}
	var warnings []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		if k := root.Content[i].Value; !known[k] {
			warnings = append(warnings, fmt.Sprintf("unknown config key %q ignored", k))
		}
	}
	var typed struct {
		Listen         *string  `yaml:"listen"`
		DataDir        *string  `yaml:"data_dir"`
		LogFile        *string  `yaml:"log_file"`
		TrustedProxies []string `yaml:"trusted_proxies"`
	}
	if err := root.Decode(&typed); err != nil {
		return nil, err
	}
	if typed.Listen != nil {
		cfg.Listen = *typed.Listen
	}
	if typed.DataDir != nil {
		cfg.DataDir = *typed.DataDir
	}
	if typed.LogFile != nil {
		cfg.LogFile = *typed.LogFile
	}
	if typed.TrustedProxies != nil {
		var w []string
		cfg.TrustedProxies, w = parseProxies(typed.TrustedProxies)
		warnings = append(warnings, w...)
	}
	return warnings, nil
}

// parseProxies reads addresses and CIDR ranges. A bad entry is skipped
// with a warning.
func parseProxies(list []string) ([]netip.Prefix, []string) {
	out := []netip.Prefix{}
	var warnings []string
	for _, raw := range list {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if p, err := netip.ParsePrefix(v); err == nil {
			out = append(out, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(v); err == nil {
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("trusted proxy %q is not an address or a CIDR range, ignored", v))
	}
	return out, warnings
}

// applyEnv applies JUKEM_LISTEN, JUKEM_DATA_DIR, JUKEM_LOG_FILE and
// JUKEM_TRUSTED_PROXIES (a comma list). A variable set to the empty string
// still applies. JUKEM_LOG_FILE="" selects stdout, which the Docker image
// uses.
func applyEnv(cfg *Config) []string {
	var warnings []string
	if v, ok := os.LookupEnv("JUKEM_LISTEN"); ok {
		cfg.Listen = v
	}
	if v, ok := os.LookupEnv("JUKEM_DATA_DIR"); ok {
		cfg.DataDir = v
	}
	if v, ok := os.LookupEnv("JUKEM_LOG_FILE"); ok {
		cfg.LogFile = v
	}
	if v, ok := os.LookupEnv("JUKEM_TRUSTED_PROXIES"); ok {
		cfg.TrustedProxies, warnings = parseProxies(strings.Split(v, ","))
	}
	return warnings
}

// Runtime reports where jukem runs: "docker" or "host". The Docker image sets
// JUKEM_RUNTIME=docker so that fix-it messages apply to a container.
func Runtime() string {
	if os.Getenv("JUKEM_RUNTIME") == "docker" {
		return "docker"
	}
	return "host"
}
