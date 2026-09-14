package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"time"

	"jukem/internal/config"
)

// runHealthcheck calls the local /healthz endpoint. The Docker image uses it
// as HEALTHCHECK so the image needs no curl or wget.
func runHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	cfgPath := fs.String("config", "/etc/jukem/config.yaml", "bootstrap config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, _, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", cfg.Listen, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", resp.Status)
	}
	return nil
}
