package mpdctl

import (
	"bytes"
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
	LastError string
}

// Supervisor runs mpd --no-daemon as a child and keeps it running. The
// loop goroutine owns the process: Reconfigure and Stop only ask it to act,
// so every exit has a known reason.
type Supervisor struct {
	paths   Paths
	binary  string
	log     *slog.Logger
	onEvent func(Event)

	mu       sync.Mutex
	conf     Config
	cmd      *exec.Cmd
	running  bool
	since    time.Time
	lastErr  string
	failures []time.Time

	restartCh chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
}

// New creates a supervisor for the MPD binary named binary ("mpd" on the
// path). onEvent receives every process event; nil is allowed.
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
	recoverOrphan(s.paths, s.binary, s.log)
	if err := s.writeConf(conf); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.conf = conf
	s.cancel = cancel
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.loop(ctx)
	return nil
}

// Reconfigure writes a new config and asks the loop to restart MPD. It
// returns at once; the restart happens in the background.
func (s *Supervisor) Reconfigure(conf Config) error {
	if err := s.writeConf(conf); err != nil {
		return err
	}
	s.mu.Lock()
	s.conf = conf
	s.mu.Unlock()
	s.Restart()
	return nil
}

// Restart asks the loop to restart MPD with the same config.
func (s *Supervisor) Restart() {
	select {
	case s.restartCh <- struct{}{}:
	default:
	}
}

// Stop ends MPD and the restart loop. The loop waits up to five seconds
// for a clean exit and then kills the process.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	os.Remove(s.paths.PIDFile)
	os.Remove(s.paths.Socket)
	s.onEvent(Event{Kind: EventStopped})
}

// Status reports the process state.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Running: s.running, Since: s.since, LastError: s.lastErr}
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

// exitReason says why runOnce returned.
type exitReason int

const (
	exitCrashed   exitReason = iota // MPD exited on its own
	exitRequested                   // Reconfigure asked for a restart
	exitCancelled                   // Stop ended the loop
)

// loop runs MPD and restarts it with backoff until ctx ends.
func (s *Supervisor) loop(ctx context.Context) {
	defer close(s.done)
	backoff := time.Second
	for {
		// A restart request from before this start is already satisfied.
		select {
		case <-s.restartCh:
		default:
		}
		start := time.Now()
		reason, err := s.runOnce(ctx)
		s.mu.Lock()
		s.running = false
		s.cmd = nil
		if err != nil && reason == exitCrashed {
			s.lastErr = err.Error()
		}
		s.mu.Unlock()
		switch reason {
		case exitCancelled:
			return
		case exitRequested:
			if ctx.Err() != nil {
				// Stop and a restart request arrived together.
				return
			}
			s.log.Info("restarting mpd with the new config")
			backoff = time.Second
			continue
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		s.log.Warn("mpd exited", "error", err, "restart_in", backoff)
		s.onEvent(Event{Kind: EventExited, Err: err})
		s.noteFailure()
		select {
		case <-ctx.Done():
			return
		case <-s.restartCh:
			backoff = time.Second
			continue
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
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

// runOnce starts MPD, waits for the socket, and returns when it exits or
// when the loop must stop or restart it.
func (s *Supervisor) runOnce(ctx context.Context) (exitReason, error) {
	os.Remove(s.paths.Socket)
	// --stderr sends MPD's log to its output, which the lineLogger reads.
	cmd := exec.Command(s.binary, "--no-daemon", "--stderr", s.paths.ConfFile)
	cmd.Stdout = &lineLogger{log: s.log}
	cmd.Stderr = &lineLogger{log: s.log}
	if err := cmd.Start(); err != nil {
		return exitCrashed, fmt.Errorf("start %s: %w", s.binary, err)
	}
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	s.writePIDFile(cmd.Process.Pid)

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	reason, err := s.waitForSocket(ctx, waitErr)
	if err != nil {
		return reason, err
	}
	s.mu.Lock()
	s.running = true
	s.since = time.Now()
	s.lastErr = ""
	s.mu.Unlock()
	s.log.Info("mpd running", "pid", cmd.Process.Pid)
	s.onEvent(Event{Kind: EventStarted})

	select {
	case err := <-waitErr:
		return exitCrashed, err
	case <-ctx.Done():
		terminate(cmd.Process, waitErr, 5*time.Second)
		return exitCancelled, nil
	case <-s.restartCh:
		terminate(cmd.Process, waitErr, 5*time.Second)
		return exitRequested, nil
	}
}

// waitForSocket polls the unix socket until MPD answers, the process
// exits, or fifteen seconds pass.
func (s *Supervisor) waitForSocket(ctx context.Context, waitErr chan error) (exitReason, error) {
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if c, err := net.Dial("unix", s.paths.Socket); err == nil {
			c.Close()
			return exitCrashed, nil
		}
		select {
		case err := <-waitErr:
			return exitCrashed, fmt.Errorf("mpd exited before its socket opened: %v", err)
		case <-ctx.Done():
			terminate(s.cmdProcess(), waitErr, 5*time.Second)
			return exitCancelled, ctx.Err()
		case <-s.restartCh:
			terminate(s.cmdProcess(), waitErr, 5*time.Second)
			return exitRequested, errors.New("restart requested during start")
		case <-timeout.C:
			terminate(s.cmdProcess(), waitErr, 5*time.Second)
			return exitCrashed, fmt.Errorf("mpd did not open %s within 15s", s.paths.Socket)
		case <-tick.C:
		}
	}
}

func (s *Supervisor) cmdProcess() *os.Process {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd.Process
}

// terminate sends SIGTERM, waits for the exit, and SIGKILLs after grace.
func terminate(p *os.Process, waitErr chan error, grace time.Duration) {
	if err := p.Signal(syscall.SIGTERM); err != nil {
		p.Kill()
		<-waitErr
		return
	}
	select {
	case <-waitErr:
	case <-time.After(grace):
		p.Kill()
		<-waitErr
	}
}

// writePIDFile records what startup recovery needs to identify the process
// on the next start: pid, kernel start time, and the config path.
func (s *Supervisor) writePIDFile(pid int) {
	content := fmt.Sprintf("%d\n%s\n%s\n", pid, processStartTime(pid), s.paths.ConfFile)
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

// lineLogger sends each line MPD prints to the service log, so MPD output
// lands in the rotating log file and in docker logs.
type lineLogger struct {
	log *slog.Logger
	buf bytes.Buffer
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		line, err := l.buf.ReadString('\n')
		if err != nil {
			l.buf.WriteString(line)
			break
		}
		l.log.Info("mpd: " + strings.TrimRight(line, "\r\n"))
	}
	return len(p), nil
}
