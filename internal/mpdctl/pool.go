package mpdctl

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/fhs/gompd/v2/mpd"
)

// ErrNotRunning reports that MPD does not answer on its socket.
var ErrNotRunning = errors.New("mpd is not running")

// ErrTimeout reports that MPD did not answer a command in time. The
// process is alive but stuck; the watchdog restarts it.
var ErrTimeout = errors.New("mpd did not answer in time")

// commandTimeout bounds one command, and the wait for a free connection.
const commandTimeout = 15 * time.Second

// pingAfter is how long a connection may rest before the pool pings it.
// MPD closes a connection that rests past connection_timeout (60 s).
const pingAfter = 30 * time.Second

// Pool holds the command connections. A wedged MPD must not hold every
// caller, so a command that does not answer in time returns ErrTimeout;
// its connection stays in use until MPD answers or is restarted.
type Pool struct {
	socket string
	sem    chan struct{}

	mu   sync.Mutex
	idle []idleConn
}

type idleConn struct {
	c    *mpd.Client
	used time.Time
}

// NewPool creates a pool of at most max connections to the unix socket.
func NewPool(socket string, max int) *Pool {
	return &Pool{socket: socket, sem: make(chan struct{}, max)}
}

// Do runs fn with a connection. After an error that is not an MPD protocol
// error the pool discards the connection, because its state is unknown.
func (p *Pool) Do(fn func(c *mpd.Client) error) error {
	select {
	case p.sem <- struct{}{}:
	case <-time.After(commandTimeout):
		return ErrTimeout
	}
	done := make(chan error, 1)
	go func() {
		defer func() { <-p.sem }()
		c, err := p.get()
		if err != nil {
			done <- err
			return
		}
		err = fn(c)
		var mpdErr mpd.Error
		if err == nil || errors.As(err, &mpdErr) {
			p.put(c)
		} else {
			c.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(commandTimeout):
		return ErrTimeout
	}
}

func (p *Pool) get() (*mpd.Client, error) {
	for {
		p.mu.Lock()
		if len(p.idle) == 0 {
			p.mu.Unlock()
			break
		}
		ic := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		p.mu.Unlock()
		if time.Since(ic.used) < pingAfter || ic.c.Ping() == nil {
			return ic.c, nil
		}
		ic.c.Close()
	}
	c, err := mpd.Dial("unix", p.socket)
	if err != nil {
		return nil, errors.Join(ErrNotRunning, err)
	}
	return c, nil
}

func (p *Pool) put(c *mpd.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.idle) < cap(p.sem) {
		p.idle = append(p.idle, idleConn{c: c, used: time.Now()})
		return
	}
	c.Close()
}

// Close drops every idle connection.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ic := range p.idle {
		ic.c.Close()
	}
	p.idle = nil
}

// Watch holds one connection in idle mode and sends each changed subsystem
// name on the returned channel. After a reconnect it sends every watched
// subsystem, because each one can have changed. It stops when ctx ends.
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
			for _, sub := range subsystems {
				select {
				case out <- sub:
				case <-ctx.Done():
				}
			}
			watchOne(ctx, w, out, log)
		}
	}()
	return out
}

func watchOne(ctx context.Context, w *mpd.Watcher, out chan<- string, log *slog.Logger) {
	defer closeWatcher(w)
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

// closeWatcher closes the watcher and drains its channels meanwhile, so a
// send inside the watcher goroutine cannot block the close.
func closeWatcher(w *mpd.Watcher) {
	done := make(chan struct{})
	go func() {
		w.Close()
		close(done)
	}()
	events, errs := w.Event, w.Error
	for {
		select {
		case <-done:
			return
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
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
		// The wanted output goes on first, so playback never sees a moment
		// with no output.
		found := false
		for _, o := range outputs {
			if o["outputname"] == name {
				id, err := strconv.Atoi(o["outputid"])
				if err != nil {
					return errors.New("mpd output without id")
				}
				found = true
				if err := c.EnableOutput(id); err != nil {
					return err
				}
			}
		}
		if !found && name != "" {
			return errors.New("output not in config: " + name)
		}
		for _, o := range outputs {
			if o["outputname"] == name {
				continue
			}
			id, err := strconv.Atoi(o["outputid"])
			if err != nil {
				return errors.New("mpd output without id")
			}
			if err := c.DisableOutput(id); err != nil {
				return err
			}
		}
		return nil
	})
}
