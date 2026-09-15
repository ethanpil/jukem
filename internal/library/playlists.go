package library

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"jukem/internal/store"
)

// Playlists keeps playlists as .m3u files in MPD's playlist directory, so
// other tools can read them and they survive a database rebuild. The
// store row gives each one a stable id.
type Playlists struct {
	dir   string
	root  func() string
	store *store.Store
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

var badName = regexp.MustCompile(`[/\\\x00-\x1f]|^\.|^\s*$`)

// checkName rejects names that cannot be a file name.
func checkName(name string) error {
	if len(name) > 100 || badName.MatchString(name) {
		return &OpError{Status: http.StatusUnprocessableEntity, Detail: "that playlist name is not allowed"}
	}
	return nil
}

func (p *Playlists) file(name string) string {
	return filepath.Join(p.dir, name+".m3u")
}

// Create makes an empty playlist.
func (p *Playlists) Create(ctx context.Context, name string) (store.Playlist, error) {
	name = strings.TrimSpace(name)
	if err := checkName(name); err != nil {
		return store.Playlist{}, err
	}
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
	if err := p.write(name, nil); err != nil {
		p.store.DeletePlaylist(ctx, pl.ID)
		return pl, err
	}
	return pl, nil
}

// Entries reads the playlist file. A row whose file is gone returns an
// empty list rather than an error.
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
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries = append(entries, filepath.ToSlash(line))
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
		w.WriteString(e)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p.file(name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Set replaces the entries.
func (p *Playlists) Set(ctx context.Context, pl store.Playlist, entries []string) error {
	clean := make([]string, 0, len(entries))
	for _, e := range entries {
		c, err := CleanRel(e)
		if err != nil || c == "" {
			return &OpError{Status: http.StatusUnprocessableEntity, Detail: fmt.Sprintf("entry %q is not allowed", e)}
		}
		clean = append(clean, c)
	}
	if err := p.write(pl.Name, clean); err != nil {
		return err
	}
	return p.store.TouchPlaylist(ctx, pl.ID)
}

// Append adds files to the end.
func (p *Playlists) Append(ctx context.Context, pl store.Playlist, files []string) (int, error) {
	entries, err := p.Entries(pl.Name)
	if err != nil {
		return 0, err
	}
	if err := p.Set(ctx, pl, append(entries, files...)); err != nil {
		return 0, err
	}
	return len(files), nil
}

// Rename changes the name and the file.
func (p *Playlists) Rename(ctx context.Context, pl store.Playlist, name string) error {
	name = strings.TrimSpace(name)
	if err := checkName(name); err != nil {
		return err
	}
	if name == pl.Name {
		return nil
	}
	if _, err := os.Lstat(p.file(name)); err == nil {
		return &OpError{Status: http.StatusConflict, Detail: "a playlist with that name exists"}
	}
	if err := p.store.RenamePlaylist(ctx, pl.ID, name); err != nil {
		if errors.Is(err, store.ErrExists) {
			return &OpError{Status: http.StatusConflict, Detail: "a playlist with that name exists"}
		}
		return err
	}
	if err := os.Rename(p.file(pl.Name), p.file(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		p.store.RenamePlaylist(ctx, pl.ID, pl.Name)
		return err
	}
	return nil
}

// Delete removes the row and the file.
func (p *Playlists) Delete(ctx context.Context, pl store.Playlist) error {
	if err := p.store.DeletePlaylist(ctx, pl.ID); err != nil {
		return err
	}
	if err := os.Remove(p.file(pl.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Referencing returns the names of playlists with an entry at path or
// below it.
func (p *Playlists) Referencing(ctx context.Context, path string) ([]string, error) {
	lists, err := p.store.ListPlaylists(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, pl := range lists {
		entries, err := p.Entries(pl.Name)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e == path || strings.HasPrefix(e, path+"/") {
				names = append(names, pl.Name)
				break
			}
		}
	}
	return names, nil
}

// Rewrite updates every playlist after old moved to new. isDir means every
// entry below old moves too. deleted means the entries go away.
func (p *Playlists) Rewrite(ctx context.Context, old, new string, isDir, deleted bool) (int, error) {
	lists, err := p.store.ListPlaylists(ctx)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, pl := range lists {
		entries, err := p.Entries(pl.Name)
		if err != nil {
			return changed, err
		}
		out := make([]string, 0, len(entries))
		hit := false
		for _, e := range entries {
			match := e == old || (isDir && strings.HasPrefix(e, old+"/"))
			if !match {
				out = append(out, e)
				continue
			}
			hit = true
			if deleted {
				continue
			}
			out = append(out, new+strings.TrimPrefix(e, old))
		}
		if !hit {
			continue
		}
		if err := p.Set(ctx, pl, out); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}
