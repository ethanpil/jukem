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

// References are the records jukem owns that point at files. A move or
// delete updates them in the same operation, so a normal thing in the UI
// never breaks a playlist or a schedule.
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
	seenP, seenS := map[string]bool{}, map[string]bool{}
	for _, rel := range paths {
		abs, clean, oe := f.resolve(root, rel)
		if oe != nil {
			return ins, oe
		}
		info, err := os.Lstat(abs)
		if err != nil {
			continue
		}
		if info.IsDir() {
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
		} else {
			ins.Files++
		}
		if refs.Playlists != nil {
			names, _ := refs.Playlists.Referencing(ctx, clean)
			for _, n := range names {
				if !seenP[n] {
					seenP[n] = true
					ins.Playlists = append(ins.Playlists, n)
				}
			}
		}
		if refs.Store != nil {
			names, _ := refs.Store.DirectoryReferences(ctx, clean)
			for _, n := range names {
				if !seenS[n] {
					seenS[n] = true
					ins.Schedules = append(ins.Schedules, n)
				}
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
	if _, err := os.Lstat(dst); err == nil {
		return &OpError{Status: http.StatusConflict, Detail: "something with that name exists at the destination"}
	}
	if err := f.ensureWritable(filepath.Dir(src), root); err != nil {
		return err
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
			return f.opError(err, filepath.Dir(src), root)
		}
		if info.IsDir() {
			return &OpError{Status: http.StatusConflict, Detail: "a folder cannot move across file systems; move its files instead"}
		}
		if err := moveAcross(src, dst); err != nil {
			return f.opError(err, destDir, root)
		}
		os.Remove(src)
	}
	f.updateReferences(ctx, refs, fromRel, toRel, info.IsDir(), false)
	f.noteChanged(path.Dir(fromRel), path.Dir(toRel))
	return nil
}

// Delete removes files and folders for good and drops the references.
func (f *Files) Delete(ctx context.Context, refs References, paths []string) error {
	root := f.root()
	var touched []string
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
		if err := os.RemoveAll(abs); err != nil {
			return f.opError(err, filepath.Dir(abs), root)
		}
		f.updateReferences(ctx, refs, clean, "", info.IsDir(), true)
		touched = append(touched, path.Dir(clean))
	}
	if len(touched) > 0 {
		f.noteChanged(touched...)
	}
	return nil
}

// updateReferences rewrites playlists, schedules and the do-not-play list.
func (f *Files) updateReferences(ctx context.Context, refs References, old, new string, isDir, deleted bool) {
	if refs.Playlists != nil {
		if _, err := refs.Playlists.Rewrite(ctx, old, new, isDir, deleted); err != nil {
			f.log.Warn("cannot rewrite playlist entries", "path", old, "error", err)
		}
	}
	if refs.Store == nil {
		return
	}
	if deleted {
		if err := refs.Store.DeleteDoNotPlayUnder(ctx, old); err != nil {
			f.log.Warn("cannot update the do-not-play list", "error", err)
		}
		return
	}
	if err := refs.Store.MoveDoNotPlay(ctx, old, new, isDir); err != nil {
		f.log.Warn("cannot update the do-not-play list", "error", err)
	}
	if isDir {
		if _, err := refs.Store.RepointDirectory(ctx, old, new); err != nil {
			f.log.Warn("cannot repoint schedules", "error", err)
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
