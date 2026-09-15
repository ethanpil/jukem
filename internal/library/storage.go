package library

import (
	"os"
	"path/filepath"
)

// Storage describes the music root's file system.
type Storage struct {
	Root       string `json:"root"`
	TotalBytes int64  `json:"total_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
	ReadOnly   bool   `json:"read_only" doc:"True when the root is not writable; uploads and file operations are hidden"`
	Missing    bool   `json:"missing" doc:"True when the root does not exist"`
}

// Stat reports the space and writability of the music root.
func Stat(root string) Storage {
	s := Storage{Root: root}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		s.Missing = true
		s.ReadOnly = true
		return s
	}
	s.TotalBytes, s.FreeBytes = diskSpace(root)
	s.ReadOnly = !Writable(root)
	return s
}

// Writable reports whether a new file can be created in dir.
func Writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".jukem-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// TempDir is the upload staging directory inside the music root. The dot
// keeps MPD from indexing it.
func TempDir(root string) string {
	return filepath.Join(root, ".jukem-tmp")
}
