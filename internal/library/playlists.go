package library

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"jukem/internal/store"
)

// Playlists keeps playlists as .m3u files in MPD's playlist directory, so
// other tools can read them and they survive a database rebuild. The
// store row gives each one a stable id. One mutex serialises every change,
// because each change reads the file and writes it back.
type Playlists struct {
	dir   string
	root  func() string
	store *store.Store
	mu    sync.Mutex
}

// NewPlaylists creates the manager over MPD's playlist directory.
func NewPlaylists(dir string, root func() string, st *store.Store) *Playlists {
	return &Playlists{dir: dir, root: root, store: st}
}

// PlaylistEntry is one line of a playlist with its state on disk.
type PlaylistEntry struct {
	File    string `json:"file"`
	Missing bool   `json:"missing" doc:"True when the file is not under the music root"`
}

// checkName rejects names that cannot be a file name. It returns the
// trimmed name.
func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	clean, err := CleanRel(name)
	if err != nil || clean == "" || clean != name || strings.Contains(clean, "/") || len(name) > 100 {
		return "", &OpError{Status: http.StatusUnprocessableEntity, Detail: "that playlist name is not allowed"}
	}
	return name, nil
}

func (p *Playlists) file(name string) string {
	return filepath.Join(p.dir, name+".m3u")
}

// Create makes an empty playlist, or one with files.
func (p *Playlists) Create(ctx context.Context, name string, files []string) (store.Playlist, error) {
	name, err := checkName(name)
	if err != nil {
		return store.Playlist{}, err
	}
	clean, err := cleanEntries(files)
	if err != nil {
		return store.Playlist{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.MkdirAll(p.dir, 0o750); err != nil {
		return store.Playlist{}, err
	}
	if _, err := os.Lstat(p.file(name)); err == nil {
		return store.Playlist{}, &OpError{Status: http.StatusConflict, Detail: "a playlist with that name exists"}
	}
	pl, err := p.store.CreatePlaylist(ctx, name)
	if errors.Is(err, store.ErrExists) {
		return pl, &OpError{Status: http.StatusConflict, Detail: "a playlist with that name exists"}
	}
	if err != nil {
		return pl, err
	}
	if err := p.write(name, clean); err != nil {
		p.store.DeletePlaylist(ctx, pl.ID)
		return pl, err
	}
	return pl, nil
}

// Entries reads the playlist file. A row whose file is gone returns an
// empty list rather than an error. A name that starts with "#" is stored
// as "./#..." so the reader does not take it for a comment.
func (p *Playlists) Entries(name string) ([]string, error) {
	f, err := os.Open(p.file(name))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			// An editor on Windows can put a byte order mark first.
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries = append(entries, strings.TrimPrefix(filepath.ToSlash(line), "./"))
	}
	return entries, sc.Err()
}

// EntriesWithState reads the playlist and marks entries whose file is not
// on disk.
func (p *Playlists) EntriesWithState(name string) ([]PlaylistEntry, error) {
	files, err := p.Entries(name)
	if err != nil {
		return nil, err
	}
	root := p.root()
	out := make([]PlaylistEntry, 0, len(files))
	for _, f := range files {
		e := PlaylistEntry{File: f}
		abs, err := Abs(root, f)
		if err != nil {
			e.Missing = true
		} else if _, err := os.Stat(abs); err != nil {
			e.Missing = true
		}
		out = append(out, e)
	}
	return out, nil
}

// PresentEntries returns the entries whose file exists, in order.
func (p *Playlists) PresentEntries(name string) ([]string, error) {
	entries, err := p.EntriesWithState(name)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.Missing {
			out = append(out, e.File)
		}
	}
	return out, nil
}

// write replaces the playlist file atomically.
func (p *Playlists) write(name string, entries []string) error {
	if err := os.MkdirAll(p.dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(p.dir, "."+name+".*.tmp")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(tmp)
	w.WriteString("#EXTM3U\n")
	for _, e := range entries {
		if strings.HasPrefix(e, "#") {
			w.WriteString("./")
		}
		w.WriteString(e)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	// The rename is atomic; the sync keeps a power cut from leaving an
	// empty file behind it.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	// Other tools read the file too.
	os.Chmod(tmp.Name(), 0o644)
	if err := os.Rename(tmp.Name(), p.file(name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func cleanEntries(entries []string) ([]string, error) {
	clean := make([]string, 0, len(entries))
	for _, e := range entries {
		c, err := CleanRel(e)
		if err != nil || c == "" {
			return nil, &OpError{Status: http.StatusUnprocessableEntity, Detail: fmt.Sprintf("entry %q is not allowed", e)}
		}
		clean = append(clean, c)
	}
	return clean, nil
}

// Set replaces the entries.
func (p *Playlists) Set(ctx context.Context, pl store.Playlist, entries []string) error {
	clean, err := cleanEntries(entries)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.write(pl.Name, clean); err != nil {
		return err
	}
	return p.store.TouchPlaylist(ctx, pl.ID)
}

// Append adds files to the end.
func (p *Playlists) Append(ctx context.Context, pl store.Playlist, files []string) error {
	clean, err := cleanEntries(files)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entries, err := p.Entries(pl.Name)
	if err != nil {
		return err
	}
	if err := p.write(pl.Name, append(entries, clean...)); err != nil {
		return err
	}
	return p.store.TouchPlaylist(ctx, pl.ID)
}

// Rename changes the name and the file. It returns the name stored.
func (p *Playlists) Rename(ctx context.Context, pl store.Playlist, name string) (string, error) {
	name, err := checkName(name)
	if err != nil {
		return "", err
	}
	if name == pl.Name {
		return name, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := os.Lstat(p.file(name)); err == nil {
		return "", &OpError{Status: http.StatusConflict, Detail: "a playlist with that name exists"}
	}
	if err := p.store.RenamePlaylist(ctx, pl.ID, name); err != nil {
		if errors.Is(err, store.ErrExists) {
			return "", &OpError{Status: http.StatusConflict, Detail: "a playlist with that name exists"}
		}
		return "", err
	}
	if err := os.Rename(p.file(pl.Name), p.file(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		p.store.RenamePlaylist(ctx, pl.ID, pl.Name)
		return "", err
	}
	return name, nil
}

// Delete removes the file and then the row, so a file that cannot be
// removed does not leave an orphan that blocks the name.
func (p *Playlists) Delete(ctx context.Context, pl store.Playlist) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.Remove(p.file(pl.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return p.store.DeletePlaylist(ctx, pl.ID)
}

// Change describes a moved or deleted path. New is empty for a delete.
type Change struct {
	Old   string
	New   string
	IsDir bool
}

// matches reports whether entry is the changed path or below it, and
// returns the rewritten entry.
func (c Change) apply(entry string) (string, bool) {
	if entry == c.Old {
		return c.New, true
	}
	if c.IsDir && strings.HasPrefix(entry, c.Old+"/") {
		if c.New == "" {
			return "", true
		}
		return c.New + strings.TrimPrefix(entry, c.Old), true
	}
	return "", false
}

// Referencing returns, for each path, the names of playlists with an
// entry at the path or below it. Every playlist file is read once.
func (p *Playlists) Referencing(ctx context.Context, paths []string) (map[string][]string, error) {
	lists, err := p.store.ListPlaylists(ctx)
	if err != nil {
		return nil, err
	}
	changes := make([]Change, len(paths))
	for i, path := range paths {
		changes[i] = Change{Old: path, IsDir: true}
	}
	out := map[string][]string{}
	for _, pl := range lists {
		entries, err := p.Entries(pl.Name)
		if err != nil {
			continue
		}
		hit := map[string]bool{}
		for _, e := range entries {
			for _, c := range changes {
				if _, ok := c.apply(e); ok && !hit[c.Old] {
					hit[c.Old] = true
					out[c.Old] = append(out[c.Old], pl.Name)
				}
			}
		}
	}
	return out, nil
}

// Rewrite updates every playlist for a set of moves and deletes in one
// pass per playlist. Entries the validator rejects stay as they are, so a
// hand-written line never stops the other playlists from being updated.
func (p *Playlists) Rewrite(ctx context.Context, changes []Change) error {
	lists, err := p.store.ListPlaylists(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []error
	for _, pl := range lists {
		entries, err := p.Entries(pl.Name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out := make([]string, 0, len(entries))
		hit := false
		for _, e := range entries {
			replaced := false
			for _, c := range changes {
				if n, ok := c.apply(e); ok {
					hit, replaced = true, true
					if n != "" {
						out = append(out, n)
					}
					break
				}
			}
			if !replaced {
				out = append(out, e)
			}
		}
		if !hit {
			continue
		}
		if err := p.write(pl.Name, out); err != nil {
			errs = append(errs, err)
			continue
		}
		p.store.TouchPlaylist(ctx, pl.ID)
	}
	return errors.Join(errs...)
}
