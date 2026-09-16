// Package update asks GitHub for the newest release of jukem. It only
// reads: it does not download a package and it does not install one. A
// person does the installation with apk, as the readme shows.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"jukem/internal/store"
)

// LatestURL is the GitHub endpoint for the newest release. The repository
// is public, so the request needs no credentials.
const LatestURL = "https://api.github.com/repos/ethanpil/jukem/releases/latest"

// Interval is the time between two automatic checks.
const Interval = 24 * time.Hour

// stateKey holds the last result in the state table.
const stateKey = "update_check"

// maxBody limits the answer that is read. The part that is used is small.
const maxBody = 1 << 20

// Release is what the last check found.
type Release struct {
	Version     string    `json:"version,omitempty" doc:"Version of the newest release, without the v"`
	URL         string    `json:"url,omitempty" doc:"Page of the release"`
	PublishedAt time.Time `json:"published_at,omitzero" doc:"When the release was published"`
	CheckedAt   time.Time `json:"checked_at,omitzero" doc:"When the last check ran"`
	Error       string    `json:"error,omitempty" doc:"Why the last check failed"`
}

// Result is the answer for the UI. Available is computed against the
// running version at each read, so an upgrade clears it without a new
// check.
//
// The name must stay different from every other type that the API
// returns: the OpenAPI registry keys a schema by the name of its type,
// and two types with one name make the server stop at the start.
type Result struct {
	Current   string `json:"current" doc:"Version that runs now"`
	Available bool   `json:"available" doc:"True when the release is newer than the running version"`
	Release
}

// Checker holds the state between the checks.
type Checker struct {
	store   *store.Store
	log     *slog.Logger
	client  *http.Client
	current string
	url     string
	// enabled reports whether the automatic check may run. It is read at
	// each turn of the loop, so a settings change applies at once.
	enabled func() bool
}

// New creates a checker for the running version.
func New(st *store.Store, log *slog.Logger, current string, enabled func() bool) *Checker {
	return &Checker{
		store:   st,
		log:     log,
		client:  &http.Client{Timeout: 15 * time.Second},
		current: current,
		url:     LatestURL,
		enabled: enabled,
	}
}

// Status returns the last result with the running version.
func (c *Checker) Status(ctx context.Context) Result {
	st := Result{Current: c.current}
	if _, err := c.store.GetState(ctx, stateKey, &st.Release); err != nil {
		c.log.Warn("cannot read the last update check", "error", err)
		return st
	}
	st.Available = Newer(st.Version, c.current)
	return st
}

// Check asks GitHub now and stores the result, also when it failed: the
// UI shows why the last check did not work.
func (c *Checker) Check(ctx context.Context) (Result, error) {
	rel, err := c.fetch(ctx)
	rel.CheckedAt = time.Now().UTC()
	if err != nil {
		// A failed check keeps the version that the last good check found,
		// so one bad network moment does not hide a known release.
		var last Release
		if _, e := c.store.GetState(ctx, stateKey, &last); e == nil {
			rel.Version, rel.URL, rel.PublishedAt = last.Version, last.URL, last.PublishedAt
		}
		rel.Error = err.Error()
	}
	if e := c.store.SetState(ctx, stateKey, rel); e != nil {
		c.log.Warn("cannot store the update check", "error", e)
	}
	st := Result{Current: c.current, Release: rel, Available: Newer(rel.Version, c.current)}
	return st, err
}

// Run checks at each interval while the setting allows it. The first check
// waits a short time, because the network is often not ready at start.
func (c *Checker) Run(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if c.enabled() && c.due(ctx) {
			if _, err := c.Check(ctx); err != nil {
				c.log.Info("cannot check for a new version", "error", err)
			}
		}
		timer.Reset(Interval)
	}
}

// due reports whether the last check is older than the interval. A restart
// therefore does not make a new request every time.
func (c *Checker) due(ctx context.Context) bool {
	var last Release
	if _, err := c.store.GetState(ctx, stateKey, &last); err != nil {
		return true
	}
	return time.Since(last.CheckedAt) >= Interval
}

// githubRelease is the part of the answer that jukem reads.
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

func (c *Checker) fetch(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "jukem/"+c.current)
	resp, err := c.client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("cannot reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var gr githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&gr); err != nil {
		return Release{}, fmt.Errorf("cannot read the answer from GitHub: %w", err)
	}
	if gr.Draft || gr.Prerelease {
		return Release{}, nil
	}
	v := strings.TrimPrefix(strings.TrimSpace(gr.TagName), "v")
	if v == "" {
		return Release{}, fmt.Errorf("the release has no tag")
	}
	return Release{Version: v, URL: gr.HTMLURL, PublishedAt: gr.PublishedAt}, nil
}

// Newer reports whether version a is after version b. A version that is
// not a number, such as the "dev" of a build from source, is never after
// another one: a developer decides alone when to change version.
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

// parse reads major.minor.patch. A suffix such as -rc1 makes the version
// unusable for a comparison, and parse refuses it.
func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return out, false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
