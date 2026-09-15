package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"jukem/internal/events"
	"jukem/internal/player"
)

// OpError is a file operation that failed for a reason the client can act
// on. Status is the HTTP status to answer with.
type OpError struct {
	Status  int
	Detail  string
	Folder  string `json:"folder,omitempty"`
	Fix     string `json:"fix,omitempty"`
	Command string `json:"command,omitempty"`
}

func (e *OpError) Error() string { return e.Detail }

// Limits are the upload rules from the settings.
type Limits struct {
	MaxBytes  int64
	Reserve   int64
	Extension func(name string) bool
}

// Files manages uploads and folders under the music root and asks MPD to
// scan what changed.
type Files struct {
	root    func() string
	limits  func() Limits
	player  *player.Player
	events  *events.Hub
	log     *slog.Logger
	runtime string

	mu        sync.Mutex
	active    int
	pending   []string // folders with new files since the last scan
	last      time.Time
	scanning  string // folder of the scan in progress, "" when none
	scanCount int
}

// NewFiles creates the manager. root and limits read the current settings.
func NewFiles(root func() string, limits func() Limits, p *player.Player, ev *events.Hub, log *slog.Logger, runtime string) *Files {
	return &Files{root: root, limits: limits, player: p, events: ev, log: log, runtime: runtime}
}

// Limits returns the current upload rules.
func (f *Files) Limits() Limits { return f.limits() }

// Run cleans leftover temporary files at start and every hour, and runs
// the rescan five seconds after the last upload finished.
func (f *Files) Run(ctx context.Context) {
	f.cleanTemp()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	clean := time.NewTicker(time.Hour)
	defer clean.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-clean.C:
			f.cleanTemp()
		case <-tick.C:
			f.maybeScan()
		}
	}
}

// cleanTemp removes .part files older than an hour from the staging dir.
func (f *Files) cleanTemp() {
	dir := filepath.Join(f.root(), ".jukem-tmp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !strings.HasSuffix(e.Name(), ".part") || time.Since(info.ModTime()) < time.Hour {
			continue
		}
		os.Remove(filepath.Join(dir, e.Name()))
	}
}

// CheckResult answers the pre-upload check.
type CheckResult struct {
	Existing    []string `json:"existing" doc:"Paths that already exist"`
	Unsupported []string `json:"unsupported" doc:"Paths with an extension that is not allowed"`
	Invalid     []string `json:"invalid" doc:"Paths that are not allowed"`
	FreeBytes   int64    `json:"free_bytes" doc:"Space an upload can use after the reserve; 0 when unknown"`
	MaxBytes    int64    `json:"max_bytes" doc:"Largest file accepted"`
	ReadOnly    bool     `json:"read_only"`
}

// Check reports which planned paths exist or are not allowed.
func (f *Files) Check(paths []string) CheckResult {
	lim := f.limits()
	root := f.root()
	st := Stat(root)
	res := CheckResult{Existing: []string{}, Unsupported: []string{}, Invalid: []string{}, MaxBytes: lim.MaxBytes, ReadOnly: st.ReadOnly}
	if st.FreeBytes > 0 {
		res.FreeBytes = max(0, st.FreeBytes-lim.Reserve)
	}
	for _, p := range paths {
		abs, err := Abs(root, p)
		if err != nil || abs == filepath.Clean(root) {
			res.Invalid = append(res.Invalid, p)
			continue
		}
		if !lim.Extension(p) {
			res.Unsupported = append(res.Unsupported, p)
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			res.Existing = append(res.Existing, p)
		}
	}
	return res
}

// UploadResult reports one stored file.
type UploadResult struct {
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Skipped bool   `json:"skipped" doc:"True when the file existed and the policy was skip"`
}

// Upload stores one file from body. size is the declared length, or -1
// when unknown. The body streams into a temporary file inside the music
// root, is synced, and is renamed into place, so MPD never sees a partial
// file.
func (f *Files) Upload(rel, conflict string, body io.Reader, size int64) (UploadResult, error) {
	lim := f.limits()
	root := f.root()
	abs, err := Abs(root, rel)
	if err != nil || abs == filepath.Clean(root) {
		return UploadResult{}, &OpError{Status: http.StatusUnprocessableEntity, Detail: "that path is not allowed"}
	}
	if !lim.Extension(rel) {
		return UploadResult{}, &OpError{Status: http.StatusUnsupportedMediaType, Detail: "that file type is not allowed"}
	}
	if size > lim.MaxBytes {
		return UploadResult{}, &OpError{Status: http.StatusRequestEntityTooLarge, Detail: fmt.Sprintf("the file is larger than the %d MB limit", lim.MaxBytes>>20)}
	}
	st := Stat(root)
	if st.ReadOnly {
		return UploadResult{}, &OpError{Status: http.StatusForbidden, Detail: "the music root is read-only"}
	}
	if size > 0 && st.FreeBytes > 0 && size+lim.Reserve > st.FreeBytes {
		return UploadResult{}, &OpError{Status: http.StatusInsufficientStorage, Detail: "not enough free space on the music disk"}
	}
	if conflict != "replace" {
		conflict = "skip"
	}
	if _, err := os.Lstat(abs); err == nil && conflict == "skip" {
		return UploadResult{Path: rel, Skipped: true}, nil
	}
	// A folder upload keeps its structure, so missing folders are created
	// below the nearest folder that exists.
	destDir := filepath.Dir(abs)
	base := nearestExisting(destDir, root)
	if err := f.ensureWritable(base, root); err != nil {
		return UploadResult{}, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return UploadResult{}, f.opError(err, base, root)
	}

	f.mu.Lock()
	f.active++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.last = time.Now()
		f.mu.Unlock()
	}()

	tmpDir := filepath.Join(root, ".jukem-tmp")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return UploadResult{}, f.permissionError(root)
	}
	tmp, n, err := writeTemp(tmpDir, body, lim.MaxBytes)
	if err != nil {
		return UploadResult{}, err
	}
	defer os.Remove(tmp)
	if err := os.Rename(tmp, abs); err != nil {
		if !isCrossDevice(err) {
			return UploadResult{}, f.opError(err, destDir, root)
		}
		// A nested mount: rename cannot cross it. The file is written again
		// as a hidden .part file in the destination folder and renamed
		// there, which keeps the rename atomic. A copy in place would be
		// exactly what MPD must never index.
		if err := f.moveAcross(tmp, abs); err != nil {
			return UploadResult{}, f.opError(err, destDir, root)
		}
	}
	f.mu.Lock()
	f.pending = append(f.pending, path.Dir(rel))
	f.mu.Unlock()
	return UploadResult{Path: rel, Bytes: n}, nil
}

// writeTemp streams body into a new .part file, capped at limit, and
// syncs it. It returns the file's path and size.
func writeTemp(tmpDir string, body io.Reader, limit int64) (string, int64, error) {
	var name [8]byte
	rand.Read(name[:])
	tmp := filepath.Join(tmpDir, hex.EncodeToString(name[:])+".part")
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", 0, &OpError{Status: http.StatusInternalServerError, Detail: "cannot create a temporary file: " + err.Error()}
	}
	// One extra byte tells a body over the limit from one exactly at it.
	n, err := io.Copy(out, io.LimitReader(body, limit+1))
	if err == nil && n > limit {
		err = &OpError{Status: http.StatusRequestEntityTooLarge, Detail: fmt.Sprintf("the file is larger than the %d MB limit", limit>>20)}
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		var oe *OpError
		if errors.As(err, &oe) {
			return "", 0, oe
		}
		return "", 0, &OpError{Status: http.StatusInternalServerError, Detail: "upload failed: " + err.Error()}
	}
	return tmp, n, nil
}

// moveAcross copies src to a hidden .part file next to dst and renames it
// into place.
func (f *Files) moveAcross(src, dst string) error {
	part := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".part")
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(part)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(part)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(part)
		return err
	}
	if err := os.Rename(part, dst); err != nil {
		os.Remove(part)
		return err
	}
	return nil
}

// maybeScan runs one MPD update once no upload has been active for five
// seconds, on the deepest folder that covers every upload since the last
// scan.
func (f *Files) maybeScan() {
	f.mu.Lock()
	if f.active > 0 || len(f.pending) == 0 || time.Since(f.last) < 5*time.Second || f.scanning != "" {
		f.mu.Unlock()
		return
	}
	folder := commonFolder(f.pending)
	count := len(f.pending)
	f.pending = nil
	f.scanning = folder
	f.scanCount = count
	f.mu.Unlock()
	if _, err := f.player.Update(folder); err != nil {
		f.log.Warn("cannot start the library scan after uploads", "folder", folder, "error", err)
		f.mu.Lock()
		f.scanning = ""
		f.mu.Unlock()
		f.events.Publish(events.Upload, folder)
	}
}

// NoteUpdate is called when MPD's database changes or an update ends.
// When the scan that followed uploads is over, clients are told.
func (f *Files) NoteUpdate(updating bool) {
	if updating {
		return
	}
	f.mu.Lock()
	folder := f.scanning
	f.scanning = ""
	f.mu.Unlock()
	if folder != "" || f.scanCount > 0 {
		f.events.Publish(events.Upload, folder)
	}
}

// commonFolder returns the deepest folder that contains every path.
func commonFolder(folders []string) string {
	if len(folders) == 0 {
		return ""
	}
	common := strings.Split(strings.Trim(folders[0], "/"), "/")
	if folders[0] == "." || folders[0] == "" {
		return ""
	}
	for _, fo := range folders[1:] {
		if fo == "." || fo == "" {
			return ""
		}
		parts := strings.Split(strings.Trim(fo, "/"), "/")
		n := 0
		for n < len(common) && n < len(parts) && common[n] == parts[n] {
			n++
		}
		common = common[:n]
		if n == 0 {
			return ""
		}
	}
	return strings.Join(common, "/")
}

// NewFolder creates a folder under the root.
func (f *Files) NewFolder(rel string) error {
	root := f.root()
	abs, err := Abs(root, rel)
	if err != nil || abs == filepath.Clean(root) {
		return &OpError{Status: http.StatusUnprocessableEntity, Detail: "that path is not allowed"}
	}
	if _, err := os.Lstat(abs); err == nil {
		return &OpError{Status: http.StatusConflict, Detail: "a file or folder with that name exists"}
	}
	parent := nearestExisting(filepath.Dir(abs), root)
	if err := f.ensureWritable(parent, root); err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return f.opError(err, parent, root)
	}
	f.events.Publish(events.Library, path.Dir(rel))
	return nil
}

// nearestExisting walks up from dir to the first folder that exists, and
// never above root.
func nearestExisting(dir, root string) string {
	root = filepath.Clean(root)
	for dir != root {
		if _, err := os.Lstat(dir); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return root
}

// PermissionReport lists the folders that the service cannot write to.
type PermissionReport struct {
	Problems []string `json:"problems" doc:"Folders relative to the root that are not writable"`
	Checked  int      `json:"checked" doc:"Folders crawled"`
	Fix      string   `json:"fix,omitempty"`
	Command  string   `json:"command,omitempty"`
}

// CheckPermissions crawls the whole tree and lists every folder that is
// not writable.
func (f *Files) CheckPermissions(ctx context.Context) (PermissionReport, error) {
	root := f.root()
	rep := PermissionReport{Problems: []string{}}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if p != root {
				rel, _ := filepath.Rel(root, p)
				rep.Problems = append(rep.Problems, filepath.ToSlash(rel))
				return fs.SkipDir
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			return fs.SkipDir
		}
		rep.Checked++
		if !canWrite(p) {
			rel, _ := filepath.Rel(root, p)
			if rel == "." {
				rel = ""
			}
			rep.Problems = append(rep.Problems, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	if len(rep.Problems) > 0 {
		fix, cmd := f.fixText(root)
		rep.Fix, rep.Command = fix, cmd
	}
	return rep, nil
}

// ensureWritable returns an OpError that names the folder and the fix
// when dir is not writable.
func (f *Files) ensureWritable(dir, root string) error {
	if canWrite(dir) {
		return nil
	}
	return f.permissionError(dir, root)
}

func (f *Files) permissionError(dir string, root ...string) *OpError {
	r := f.root()
	if len(root) > 0 {
		r = root[0]
	}
	rel, err := filepath.Rel(r, dir)
	if err != nil || rel == "." {
		rel = ""
	}
	fix, cmd := f.fixText(filepath.Join(r, rel))
	return &OpError{
		Status:  http.StatusForbidden,
		Detail:  fmt.Sprintf("jukem cannot write to the folder %q", filepath.ToSlash(rel)),
		Folder:  filepath.ToSlash(rel),
		Fix:     fix,
		Command: cmd,
	}
}

// opError maps a file system error to an OpError.
func (f *Files) opError(err error, dir, root string) error {
	if errors.Is(err, fs.ErrPermission) {
		return f.permissionError(dir, root)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return &OpError{Status: http.StatusNotFound, Detail: "no such folder"}
	}
	return &OpError{Status: http.StatusInternalServerError, Detail: err.Error()}
}

// fixText explains how to repair ownership. The command runs on the
// Docker host when jukem runs in a container, because the container has
// no access to Docker.
func (f *Files) fixText(dir string) (fix, command string) {
	note := " A recursive chown is the wrong move on a NAS mount with its own UID mapping: fix the export or the mount options instead."
	if f.runtime == "docker" {
		return "Run this on the Docker host, not in the container. Give the music directory to the container user, or set user: in compose.yaml to the owner of the music directory." + note,
			"chown -R 1000:1000 /srv/jukem/music"
	}
	return "Give the folder to the service user." + note, "chown -R jukem:jukem " + dir
}

// FolderCount counts the files and folders below rel, for a delete
// confirmation.
func (f *Files) FolderCount(rel string) (files, folders int, err error) {
	abs, err := Abs(f.root(), rel)
	if err != nil {
		return 0, 0, err
	}
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != abs {
				folders++
			}
		} else {
			files++
		}
		return nil
	})
	return files, folders, err
}
