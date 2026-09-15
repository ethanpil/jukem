package library

import (
	"errors"
	"os"
	"sync"
	"time"
)

// Storage describes the music root's file system.
type Storage struct {
	Root       string `json:"root"`
	TotalBytes int64  `json:"total_bytes" doc:"0 when unknown"`
	FreeBytes  int64  `json:"free_bytes" doc:"0 when unknown"`
	ReadOnly   bool   `json:"read_only" doc:"True when the root is not writable. The UI hides uploads and file operations."`
	Missing    bool   `json:"missing" doc:"True when the root does not exist"`
	Problem    string `json:"problem,omitempty" doc:"Why the root is not usable, in plain words"`
}

// statCache keeps the last result for a short time, because the UI asks
// on every library load and the probe touches the directory.
var statCache struct {
	mu   sync.Mutex
	root string
	at   time.Time
	s    Storage
}

const statCacheLife = 10 * time.Second

// Stat reports the space and writability of the music root.
func Stat(root string) Storage {
	statCache.mu.Lock()
	defer statCache.mu.Unlock()
	if statCache.root == root && time.Since(statCache.at) < statCacheLife {
		return statCache.s
	}
	s := stat(root)
	statCache.root, statCache.at, statCache.s = root, time.Now(), s
	return s
}

// ForgetStat drops the cached result, for a root or permission change.
func ForgetStat() {
	statCache.mu.Lock()
	statCache.at = time.Time{}
	statCache.mu.Unlock()
}

func stat(root string) Storage {
	s := Storage{Root: root}
	st, err := os.Stat(root)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.Missing, s.ReadOnly = true, true
		s.Problem = "The music root does not exist."
		return s
	case err != nil:
		s.ReadOnly = true
		s.Problem = "The music root cannot be read: " + err.Error()
		return s
	case !st.IsDir():
		s.ReadOnly = true
		s.Problem = "The music root is not a directory."
		return s
	}
	s.TotalBytes, s.FreeBytes = diskSpace(root)
	s.ReadOnly = !Writable(root)
	if s.ReadOnly {
		s.Problem = "The music root is not writable by the service user. A read-only mount is expected to show this. For an ownership problem, Settings > Maintenance > Check library permissions gives the fix."
	}
	return s
}

// Writable reports whether a new file can be created in dir and removed
// again.
func Writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".jukem-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	return os.Remove(name) == nil
}
