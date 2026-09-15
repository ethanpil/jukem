package watchdog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"jukem/internal/events"
	"jukem/internal/store"
)

// Webhook is where alerts go besides the UI. An empty URL disables it.
type Webhook struct {
	URL    string
	Preset string // "generic" or "ntfy"
}

// Alerter records alerts and delivers new ones to the webhook.
type Alerter struct {
	store   *store.Store
	events  *events.Hub
	log     *slog.Logger
	webhook func() Webhook
	client  *http.Client
}

// NewAlerter creates an alerter. webhook is read at each delivery, so a
// settings change applies at once.
func NewAlerter(st *store.Store, ev *events.Hub, log *slog.Logger, webhook func() Webhook) *Alerter {
	client := &http.Client{
		Timeout: 10 * time.Second,
		// A redirect would turn the POST into a GET without the body.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Alerter{store: st, events: ev, log: log, webhook: webhook, client: client}
}

// Raise records a problem. A problem already active only updates its
// count; a new one is delivered to the webhook.
func (a *Alerter) Raise(ctx context.Context, kind, message, fix string) {
	al, created, err := a.store.RaiseAlert(ctx, kind, message, fix)
	if err != nil {
		a.log.Error("cannot record an alert", "kind", kind, "error", err)
		return
	}
	if !created {
		return
	}
	a.events.Publish(events.Alerts, "")
	a.log.Warn("alert raised", "kind", kind, "message", message)
	// The caller may be the scheduler loop; a slow webhook must not hold it.
	go func() {
		if err := a.deliver(context.WithoutCancel(ctx), al); err != nil {
			a.log.Warn("cannot deliver the alert to the webhook", "kind", kind, "error", err)
		}
	}()
}

// Clear ends the active alert of a kind because the problem is gone.
func (a *Alerter) Clear(ctx context.Context, kind string) {
	ok, err := a.store.ResolveAlert(ctx, kind)
	if err != nil {
		a.log.Error("cannot resolve an alert", "kind", kind, "error", err)
		return
	}
	if ok {
		a.log.Info("alert cleared", "kind", kind)
		a.events.Publish(events.Alerts, "")
	}
}

// Dismiss ends an alert at a person's request.
func (a *Alerter) Dismiss(ctx context.Context, id int64) (bool, error) {
	ok, err := a.store.DismissAlert(ctx, id)
	if ok {
		a.events.Publish(events.Alerts, "")
	}
	return ok, err
}

// Test sends a test alert to the webhook without recording it.
func (a *Alerter) Test(ctx context.Context) error {
	if a.webhook().URL == "" {
		return fmt.Errorf("no webhook URL is set")
	}
	return a.deliver(ctx, store.Alert{Kind: "test", Message: "This is a test alert from jukem.", RaisedAt: time.Now()})
}

// deliver posts the alert. The generic preset sends the alert as JSON;
// the ntfy preset sends the message as the body with ntfy's headers.
func (a *Alerter) deliver(ctx context.Context, al store.Alert) error {
	w := a.webhook()
	if w.URL == "" {
		return nil
	}
	host, _ := os.Hostname()
	var body io.Reader
	var contentType string
	title := "jukem: " + strings.ReplaceAll(al.Kind, "_", " ")
	if w.Preset == "ntfy" {
		text := al.Message
		if al.Fix != "" {
			text += "\n\n" + al.Fix
		}
		body, contentType = strings.NewReader(text), "text/plain; charset=utf-8"
	} else {
		raw, err := json.Marshal(map[string]any{
			"source": "jukem", "host": host, "kind": al.Kind, "title": title,
			"message": al.Message, "fix": al.Fix, "raised_at": al.RaisedAt, "count": al.Count,
		})
		if err != nil {
			return err
		}
		body, contentType = bytes.NewReader(raw), "application/json"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "jukem")
	if w.Preset == "ntfy" {
		req.Header.Set("Title", title+" on "+host)
		req.Header.Set("Priority", "high")
		req.Header.Set("Tags", "warning")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook answered %s; a redirect is not followed, use the final URL", resp.Status)
	}
	return nil
}
