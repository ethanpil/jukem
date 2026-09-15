package mpdctl

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// EventKind says what happened to the MPD process.
type EventKind string

const (
	// EventStarted fires when MPD answers on its socket.
	EventStarted EventKind = "started"
	// EventExited fires when MPD exits on its own.
	EventExited EventKind = "exited"
	// EventUnstable fires after five failures in five minutes.
	EventUnstable EventKind = "unstable"
	// EventStopped fires when the supervisor stops MPD on purpose.
	EventStopped EventKind = "stopped"
)

// Event is a change in the MPD process.
type Event struct {
	Kind EventKind
	Err  error
}

// Status describes the MPD process.
type Status struct {
	Running   bool
	Since     time.Time
	PID       int
	Restarts  int
	LastError string
}

// Supervisor runs mpd --no-daemon as a child and keeps it running.
type Supervisor struct {
	paths   Paths
	binary  string
	log     *slog.Logger
	onEvent func(Event)

	mu        sync.Mutex
	conf      Config
	cmd       *exec.Cmd
	running   bool
	since     time.Time
	restarts  int
	lastErr   string
	failures  []time.Time
	restartCh chan struct{}
	stopping  bool
	cancel    context.CancelFunc
	done      chan struct{}
}

// New creates a supervisor for the MPD binary named binary ("mpd" on the
// path). onEvent receives every process event; it may be nil.
func New(dataDir, binary string, log *slog.Logger, onEvent func(Event)) *Supervisor {
	if onEvent == nil {
		onEvent = func(Event) {}
	}
	return &Supervisor{
		paths:     PathsFor(dataDir),
		binary:    binary,
		log:       log,
		onEvent:   onEvent,
		restartCh: make(chan struct{}, 1),
	}
}

// Paths returns the MPD file locations.
func (s *Supervisor) Paths() Paths { return s.paths }

// Start writes the config, removes an orphan from a previous jukem, and
// runs MPD until Stop is called. It returns after the first start attempt;
// the supervisor keeps restarting MPD in the background.
func (s *Supervisor) Start(ctx context.Context, conf Config) error {
	if err := os.MkdirAll(s.paths.Dir, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(s.paths.PlaylistDir, 0o750); err != nil {
		return err
	}
	if err := s.writeConf(conf); err != nil {
		return err
	}
	recoverOrphan(s.paths, s.binary, s.log)
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.conf = conf
	s.cancel = cancel
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.loop(ctx)
	return nil
}

// Reconfigure writes a new config and restarts MPD so it takes effect.
func (s *Supervisor) Reconfigure(conf Config) error {
	if err := s.writeConf(conf); err != nil {
		return err
	}
	s.mu.Lock()
	s.conf = conf
	cmd := s.cmd
	s.mu.Unlock()
	select {
	case s.restartCh <- struct{}{}:
	default:
	}
	if cmd != nil && cmd.Process != nil {
		terminate(cmd.Process, 5*time.Second)
	}
	return nil
}

// Stop ends MPD and the restart loop. It waits up to five seconds for a
// clean exit and then kills the process.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	s.stopping = true
	cancel := s.cancel
	cmd := s.cmd
	done := s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if cmd != nil && cmd.Process != nil {
		terminate(cmd.Process, 5*time.Second)
	}
	<-done
	os.Remove(s.paths.PIDFile)
	os.Remove(s.paths.Socket)
	s.onEvent(Event{Kind: EventStopped})
}

// Status reports the process state.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Running: s.running, Since: s.since, Restarts: s.restarts, LastError: s.lastErr}
	if s.cmd != nil && s.cmd.Process != nil {
		st.PID = s.cmd.Process.Pid
	}
	return st
}

func (s *Supervisor) writeConf(conf Config) error {
	tmp := s.paths.ConfFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(conf.Render()), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, s.paths.ConfFile)
}

// loop runs MPD and restarts it with backoff until ctx ends.
func (s *Supervisor) loop(ctx context.Context) {
	defer close(s.done)
	backoff := time.Second
	for {
		start := time.Now()
		err := s.runOnce(ctx)
		s.mu.Lock()
		s.running = false
		s.cmd = nil
		if err != nil {
			s.lastErr = err.Error()
		}
		s.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		requested := false
		select {
		case <-s.restartCh:
			requested = true
		default:
		}
		if requested {
			s.log.Info("restarting mpd with the new config")
			backoff = time.Second
			continue
		}
		s.log.Warn("mpd exited", "error", err, "restart_in", backoff)
		s.onEvent(Event{Kind: EventExited, Err: err})
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		s.noteFailure()
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		s.mu.Lock()
		s.restarts++
		s.mu.Unlock()
	}
}

// noteFailure raises EventUnstable after five failures in five minutes.
func (s *Supervisor) noteFailure() {
	now := time.Now()
	s.mu.Lock()
	kept := s.failures[:0]
	for _, t := range s.failures {
		if now.Sub(t) < 5*time.Minute {
			kept = append(kept, t)
		}
	}
	s.failures = append(kept, now)
	n := len(s.failures)
	s.mu.Unlock()
	if n == 5 {
		s.onEvent(Event{Kind: EventUnstable, Err: errors.New("mpd failed five times in five minutes")})
	}
}

// runOnce starts MPD, waits for the socket, and returns when it exits.
func (s *Supervisor) runOnce(ctx context.Context) error {
	os.Remove(s.paths.Socket)
	cmd := exec.Command(s.binary, "--no-daemon", s.paths.ConfFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", s.binary, err)
	}
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	s.writePIDFile(cmd.Process.Pid)

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	if err := waitForSocket(ctx, s.paths.Socket, waitErr, 15*time.Second); err != nil {
		select {
		case werr := <-waitErr:
			return fmt.Errorf("mpd exited before its socket opened: %v", werr)
		default:
		}
		terminate(cmd.Process, 5*time.Second)
		<-waitErr
		return err
	}
	s.mu.Lock()
	s.running = true
	s.since = time.Now()
	s.lastErr = ""
	s.mu.Unlock()
	s.log.Info("mpd running", "pid", cmd.Process.Pid)
	s.onEvent(Event{Kind: EventStarted})
	return <-waitErr
}

// waitForSocket polls the unix socket until MPD answers, the process exits,
// or the timeout passes. It does not consume from waitErr.
func waitForSocket(ctx context.Context, socket string, waitErr <-chan error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", socket, time.Second); err == nil {
			c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		if len(waitErr) > 0 {
			return errors.New("mpd exited before its socket opened")
		}
	}
	return fmt.Errorf("mpd did not open %s within %s", socket, timeout)
}

// writePIDFile records what startup recovery needs to identify the process
// on the next start: pid, kernel start time, and the config path.
func (s *Supervisor) writePIDFile(pid int) {
	startTime := processStartTime(pid)
	content := fmt.Sprintf("%d\n%s\n%s\n", pid, startTime, s.paths.ConfFile)
	if err := os.WriteFile(s.paths.PIDFile, []byte(content), 0o640); err != nil {
		s.log.Warn("cannot write mpd pid file", "error", err)
	}
}

// pidRecord is the parsed pid file.
type pidRecord struct {
	PID       int
	StartTime string
	ConfFile  string
}

func readPIDFile(path string) (pidRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pidRecord{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 {
		return pidRecord{}, errors.New("pid file is incomplete")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return pidRecord{}, err
	}
	return pidRecord{PID: pid, StartTime: strings.TrimSpace(lines[1]), ConfFile: strings.TrimSpace(lines[2])}, nil
}

// terminate sends SIGTERM, waits, and then SIGKILLs the process.
func terminate(p *os.Process, grace time.Duration) {
	if err := p.Signal(syscall.SIGTERM); err != nil {
		p.Kill()
		return
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !processAlive(p.Pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.Kill()
}
