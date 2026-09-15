package library

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"jukem/internal/events"
	"jukem/internal/store"
)

// References are the records jukem owns that point at files. A rename,
// move or delete updates them in the same operation, so a normal action
// in the UI never breaks a playlist or a schedule.
type References struct {
	Playlists *Playlists
	Store     *store.Store
}

// Inspection says what a delete or move touches.
type Inspection struct {
	Files     int      `json:"files"`
	Folders   int      `json:"folders"`
	Playlists []string `json:"playlists" doc:"Playlists with an entry among the paths"`
	Schedules []string `json:"schedules" doc:"Rules and exceptions whose folder is among the paths"`
}

// Inspect counts what the paths hold and which records refer to them.
func (f *Files) Inspect(ctx context.Context, refs References, paths []string) (Inspection, error) {
	root := f.root()
	ins := Inspection{Playlists: []string{}, Schedules: []string{}}
	var cleaned []string
	for _, rel := range paths {
		abs, clean, oe := f.resolve(root, rel)
		if oe != nil {
			return ins, oe
		}
		info, err := os.Lstat(abs)
		if err != nil {
			continue
		}
		cleaned = append(cleaned, clean)
		if !info.IsDir() {
			ins.Files++
			continue
		}
		ins.Folders++
		filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == abs {
				return nil
			}
			if d.IsDir() {
				ins.Folders++
			} else {
				ins.Files++
			}
			return nil
		})
	}
	byPath, err := refs.Playlists.Referencing(ctx, cleaned)
	if err != nil {
		return ins, err
	}
	seen := map[string]bool{}
	for _, names := range byPath {
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				ins.Playlists = append(ins.Playlists, n)
			}
		}
	}
	seen = map[string]bool{}
	for _, clean := range cleaned {
		names, err := refs.Store.DirectoryReferences(ctx, clean)
		if err != nil {
			return ins, err
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				ins.Schedules = append(ins.Schedules, n)
			}
		}
	}
	return ins, nil
}

// Move renames or moves a file or folder to a new relative path and
// updates the references. The destination must not exist.
func (f *Files) Move(ctx context.Context, refs References, from, to string) error {
	root := f.root()
	src, fromRel, oe := f.resolve(root, from)
	if oe != nil {
		return oe
	}
	dst, toRel, oe := f.resolve(root, to)
	if oe != nil {
		return oe
	}
	if fromRel == toRel {
		return nil
	}
	if strings.HasPrefix(toRel, fromRel+"/") {
		return &OpError{Status: http.StatusUnprocessableEntity, Detail: "a folder cannot move into itself"}
	}
	info, err := os.Lstat(src)
	if err != nil {
		return &OpError{Status: http.StatusNotFound, Detail: "no such file or folder"}
	}
	// A symlinked folder moves as a link, but its references are folder
	// references.
	isDir := info.IsDir()
	if info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Stat(src); err == nil {
			isDir = target.IsDir()
		}
	}
	// On a file system without case, a rename that only changes the case
	// finds the source itself at the destination.
	if dstInfo, err := os.Lstat(dst); err == nil && !os.SameFile(info, dstInfo) {
		return &OpError{Status: http.StatusConflict, Detail: "something with that name exists at the destination"}
	}
	if err := f.ensureWritable(filepath.Dir(src), root); err != nil {
		return err
	}
	// A folder that moves to another parent needs write access to itself,
	// because its ".." entry changes.
	if isDir && filepath.Dir(src) != filepath.Dir(dst) {
		if err := f.ensureWritable(src, root); err != nil {
			return err
		}
	}
	destDir := filepath.Dir(dst)
	base := nearestExisting(destDir, root)
	if err := f.ensureWritable(base, root); err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return f.opError(err, base, root)
	}
	if err := os.Rename(src, dst); err != nil {
		if !isCrossDevice(err) {
			removeEmptyUpTo(destDir, base)
			return f.opError(err, filepath.Dir(src), root)
		}
		if info.IsDir() {
			removeEmptyUpTo(destDir, base)
			return &OpError{Status: http.StatusConflict, Detail: "a folder cannot move across file systems; move its files instead"}
		}
		if err := moveAcross(src, dst); err != nil {
			removeEmptyUpTo(destDir, base)
			return f.opError(err, destDir, root)
		}
		os.Remove(src)
	}
	f.updateReferences(ctx, refs, []Change{{Old: fromRel, New: toRel, IsDir: isDir}})
	f.noteChanged(path.Dir(fromRel), path.Dir(toRel))
	return nil
}

// removeEmptyUpTo removes the empty folders a failed move created.
func removeEmptyUpTo(dir, stop string) {
	for dir != stop && dir != filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// Delete removes files and folders for good and drops the references.
// Every folder in a tree is checked first, so a delete either completes
// or changes nothing.
func (f *Files) Delete(ctx context.Context, refs References, paths []string) error {
	root := f.root()
	type target struct {
		abs, rel string
		isDir    bool
	}
	var targets []target
	for _, rel := range paths {
		abs, clean, oe := f.resolve(root, rel)
		if oe != nil {
			return oe
		}
		info, err := os.Lstat(abs)
		if err != nil {
			continue
		}
		if err := f.ensureWritable(filepath.Dir(abs), root); err != nil {
			return err
		}
		if info.IsDir() {
			if err := f.ensureTreeWritable(abs, root); err != nil {
				return err
			}
		}
		targets = append(targets, target{abs, clean, info.IsDir()})
	}
	var changes []Change
	var touched []string
	for _, t := range targets {
		if err := os.RemoveAll(t.abs); err != nil {
			f.updateReferences(ctx, refs, changes)
			f.noteChanged(touched...)
			return f.opError(err, filepath.Dir(t.abs), root)
		}
		changes = append(changes, Change{Old: t.rel, IsDir: t.isDir})
		touched = append(touched, path.Dir(t.rel))
	}
	f.updateReferences(ctx, refs, changes)
	if len(touched) > 0 {
		f.noteChanged(touched...)
	}
	return nil
}

// ensureTreeWritable checks every folder below dir.
func (f *Files) ensureTreeWritable(dir, root string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return f.permissionError(filepath.Dir(p), root)
		}
		if d.IsDir() && !canWrite(p) {
			return f.permissionError(p, root)
		}
		return nil
	})
}

// updateReferences rewrites playlists, schedules and the do-not-play list.
func (f *Files) updateReferences(ctx context.Context, refs References, changes []Change) {
	if len(changes) == 0 {
		return
	}
	if err := refs.Playlists.Rewrite(ctx, changes); err != nil {
		f.log.Warn("cannot rewrite every playlist", "error", err)
	}
	for _, c := range changes {
		if c.New == "" {
			if err := refs.Store.DeleteDoNotPlayUnder(ctx, c.Old); err != nil {
				f.log.Warn("cannot update the do-not-play list", "error", err)
			}
			continue
		}
		if err := refs.Store.MoveDoNotPlay(ctx, c.Old, c.New, c.IsDir); err != nil {
			f.log.Warn("cannot update the do-not-play list", "error", err)
		}
		if c.IsDir {
			if _, err := refs.Store.RepointDirectory(ctx, c.Old, c.New); err != nil {
				f.log.Warn("cannot repoint schedules", "error", err)
			}
		}
	}
}

// noteChanged records folders that need a scan and publishes the change.
func (f *Files) noteChanged(folders ...string) {
	for _, fo := range folders {
		f.noteUploaded(fo)
	}
	f.events.Publish(events.Library, "")
}
