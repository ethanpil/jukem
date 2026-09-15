package mpdctl

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/fhs/gompd/v2/mpd"
)

// ErrNotRunning reports that MPD does not answer on its socket.
var ErrNotRunning = errors.New("mpd is not running")

// Pool hands out command connections. MPD closes a connection that is idle
// past connection_timeout (60 seconds), so a connection that has rested is
// pinged before use and replaced when the ping fails.
type Pool struct {
	socket string
	max    int

	mu   sync.Mutex
	idle []*pooled
	sem  chan struct{}
}

type pooled struct {
	c    *mpd.Client
	last time.Time
}

// NewPool creates a pool of at most max connections to the unix socket.
func NewPool(socket string, max int) *Pool {
	return &Pool{socket: socket, max: max, sem: make(chan struct{}, max)}
}

// Do runs fn with a connection. A connection is discarded after an error,
// because the MPD protocol state is then unknown.
func (p *Pool) Do(fn func(c *mpd.Client) error) error {
	p.sem <- struct{}{}
	defer func() { <-p.sem }()

	c, err := p.get()
	if err != nil {
		return err
	}
	if err := fn(c.c); err != nil {
		var mpdErr mpd.Error
		if errors.As(err, &mpdErr) {
			// A protocol error leaves the connection usable.
			p.put(c)
			return err
		}
		c.c.Close()
		return err
	}
	p.put(c)
	return nil
}

func (p *Pool) get() (*pooled, error) {
	p.mu.Lock()
	for len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		if time.Since(c.last) < 20*time.Second {
			p.mu.Unlock()
			return c, nil
		}
		if c.c.Ping() == nil {
			p.mu.Unlock()
			return c, nil
		}
		c.c.Close()
	}
	p.mu.Unlock()
	c, err := mpd.Dial("unix", p.socket)
	if err != nil {
		return nil, errors.Join(ErrNotRunning, err)
	}
	return &pooled{c: c}, nil
}

func (p *Pool) put(c *pooled) {
	c.last = time.Now()
	p.mu.Lock()
	if len(p.idle) < p.max {
		p.idle = append(p.idle, c)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	c.c.Close()
}

// Close drops every idle connection.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.idle {
		c.c.Close()
	}
	p.idle = nil
}

// Watch holds one connection in idle mode and sends each changed subsystem
// name on the returned channel. It reconnects while MPD restarts and stops
// when ctx ends.
func Watch(ctx context.Context, socket string, log *slog.Logger, subsystems ...string) <-chan string {
	out := make(chan string, 16)
	go func() {
		defer close(out)
		for ctx.Err() == nil {
			w, err := mpd.NewWatcher("unix", socket, "", subsystems...)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			// After a reconnect every subsystem may have changed.
			select {
			case out <- "reconnect":
			default:
			}
			watchOne(ctx, w, out, log)
		}
	}()
	return out
}

func watchOne(ctx context.Context, w *mpd.Watcher, out chan<- string, log *slog.Logger) {
	defer w.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Event:
			if !ok {
				return
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		case err, ok := <-w.Error:
			if !ok {
				return
			}
			log.Debug("mpd idle connection lost", "error", err)
			return
		}
	}
}

// EnableOnly enables the output named name and disables every other one.
// MPD numbers outputs by config position, so the name is what is stable.
func EnableOnly(p *Pool, name string) error {
	return p.Do(func(c *mpd.Client) error {
		outputs, err := c.ListOutputs()
		if err != nil {
			return err
		}
		found := false
		for _, o := range outputs {
			id, _ := parseInt(o["outputid"])
			if o["outputname"] == name {
				found = true
				if err := c.EnableOutput(id); err != nil {
					return err
				}
			} else if err := c.DisableOutput(id); err != nil {
				return err
			}
		}
		if !found && name != "" {
			return errors.New("output not in config: " + name)
		}
		return nil
	})
}

func parseInt(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errors.New("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number: " + s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
