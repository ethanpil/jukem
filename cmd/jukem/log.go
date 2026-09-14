package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

// maxLogSize is the size at which the log file rotates. jukem keeps one
// rotated copy, so the log uses at most twice this on an SD card.
const maxLogSize = 5 << 20

// rotatingFile is a size-capped log file. When the file grows past
// maxLogSize, jukem renames it to <name>.1 and starts a new file.
type rotatingFile struct {
	mu   sync.Mutex
	name string
	f    *os.File
	size int64
}

func openLogFile(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
}

func openRotatingFile(name string) (*rotatingFile, error) {
	f, err := openLogFile(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rotatingFile{name: name, f: f, size: st.Size()}, nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > maxLogSize {
		if err := r.rotate(); err != nil {
			// Keep the current file. The size stays correct, so the next
			// write tries again.
			fmt.Fprintln(os.Stderr, "jukem: log rotation failed:", err)
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate renames the current file and opens a new one. The old handle stays
// open until the new file exists, so a failure never loses the log.
func (r *rotatingFile) rotate() error {
	if err := os.Rename(r.name, r.name+".1"); err != nil {
		return err
	}
	f, err := openLogFile(r.name)
	if err != nil {
		return err
	}
	r.f.Close()
	r.f = f
	r.size = 0
	return nil
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

// newLogger logs to the file, or to stdout when the name is empty.
func newLogger(logFile string) (*slog.Logger, io.Closer, error) {
	if logFile == "" {
		return slog.New(slog.NewTextHandler(os.Stdout, nil)), io.NopCloser(nil), nil
	}
	rf, err := openRotatingFile(logFile)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	return slog.New(slog.NewTextHandler(rf, nil)), rf, nil
}
