// Package config loads the bootstrap settings. Everything else lives in the
// database and is set from the web UI.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the bootstrap settings read from /etc/jukem/config.yaml.
type Config struct {
	Listen  string `yaml:"listen"`
	DataDir string `yaml:"data_dir"`
	LogFile string `yaml:"log_file"`
}

// Default returns the settings that apply when a key is missing.
func Default() Config {
	return Config{
		Listen:  ":80",
		DataDir: "/var/lib/jukem",
		LogFile: "/var/log/jukem/jukem.log",
	}
}

// Load reads the file at path, applies the environment overrides, and returns
// the result with a warning for each unknown key. A missing file is not an
// error: the defaults and the environment apply.
func Load(path string) (Config, []string, error) {
	cfg := Default()
	var warnings []string

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		w, err := parse(data, &cfg)
		if err != nil {
			return cfg, nil, fmt.Errorf("parse %s: %w", path, err)
		}
		warnings = append(warnings, w...)
	case os.IsNotExist(err):
		warnings = append(warnings, fmt.Sprintf("config file %s not found, using defaults", path))
	default:
		return cfg, nil, fmt.Errorf("read %s: %w", path, err)
	}

	applyEnv(&cfg)
	return cfg, warnings, nil
}

// parse decodes YAML into cfg and reports unknown keys.
func parse(data []byte, cfg *Config) ([]string, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var warnings []string
	known := map[string]bool{"listen": true, "data_dir": true, "log_file": true}
	for k := range raw {
		if !known[k] {
			warnings = append(warnings, fmt.Sprintf("unknown config key %q ignored", k))
		}
	}
	// A second pass with the typed struct applies only the known keys. A key
	// that is present but null keeps its default.
	var typed struct {
		Listen  *string `yaml:"listen"`
		DataDir *string `yaml:"data_dir"`
		LogFile *string `yaml:"log_file"`
	}
	if err := yaml.Unmarshal(data, &typed); err != nil {
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
	return warnings, nil
}

// applyEnv applies JUKEM_LISTEN, JUKEM_DATA_DIR and JUKEM_LOG_FILE. A variable
// that is set to the empty string still applies: JUKEM_LOG_FILE="" selects
// stdout, which is what the Docker image uses.
func applyEnv(cfg *Config) {
	if v, ok := os.LookupEnv("JUKEM_LISTEN"); ok {
		cfg.Listen = v
	}
	if v, ok := os.LookupEnv("JUKEM_DATA_DIR"); ok {
		cfg.DataDir = v
	}
	if v, ok := os.LookupEnv("JUKEM_LOG_FILE"); ok {
		cfg.LogFile = v
	}
}

// Runtime reports where jukem runs: "docker" or "host". The Docker image sets
// JUKEM_RUNTIME=docker so that fix-it messages are phrased for a container.
func Runtime() string {
	if os.Getenv("JUKEM_RUNTIME") == "docker" {
		return "docker"
	}
	return "host"
}
