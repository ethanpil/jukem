package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

// maxLogSize is the size at which the log file rotates. One rotated copy is
// kept, so the log never uses more than twice this on an SD card.
const maxLogSize = 5 << 20

// rotatingFile is a size-capped log file. When it grows past maxLogSize it is
// renamed to <name>.1 and a new file is started.
type rotatingFile struct {
	mu   sync.Mutex
	name string
	f    *os.File
	size int64
}

func openRotatingFile(name string) (*rotatingFile, error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
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
		r.f.Close()
		os.Rename(r.name, r.name+".1")
		f, err := os.OpenFile(r.name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
		if err != nil {
			return 0, err
		}
		r.f = f
		r.size = 0
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
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
