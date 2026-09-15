package library

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DirEntry is one subdirectory in a listing.
type DirEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Writable bool   `json:"writable"`
}

// DirListing is a server-side directory listing, for choosing the music
// root.
type DirListing struct {
	Path    string     `json:"path"`
	Parent  *string    `json:"parent,omitempty" doc:"Absent at the file system root"`
	Entries []DirEntry `json:"entries"`
}

// ListDirs lists the subdirectories of an absolute path. Hidden
// directories are left out.
func ListDirs(path string) (DirListing, error) {
	if path == "" {
		path = "/"
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return DirListing{}, os.ErrInvalid
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return DirListing{}, err
	}
	out := DirListing{Path: filepath.ToSlash(path), Entries: []DirEntry{}}
	if parent := filepath.Dir(path); parent != path {
		p := filepath.ToSlash(parent)
		out.Parent = &p
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(path, e.Name())
		out.Entries = append(out.Entries, DirEntry{Name: e.Name(), Path: filepath.ToSlash(full), Writable: Writable(full)})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}
