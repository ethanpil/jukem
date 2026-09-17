package library

import (
	"context"
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
	"syscall"
	"time"

	"jukem/internal/events"
	"jukem/internal/player"
)

// OpError is a file operation that failed for a reason the client can act
// on. Status is the HTTP status to answer with.
type OpError struct {
	Status  int
	Detail  string
	Folder  string
	Fix     string
	Command string
}

func (e *OpError) Error() string { return e.Detail }

// Limits are the upload rules from the settings.
type Limits struct {
	MaxBytes  int64
	Reserve   int64
	Extension func(name string) bool
}

// scanTimeout ends the wait for a scan whose end event never came.
const scanTimeout = 10 * time.Minute

// Files manages uploads and folders under the music root and asks MPD to
// scan what changed.
type Files struct {
	root    func() string
	limits  func() Limits
	player  *player.Player
	events  *events.Hub
	log     *slog.Logger
	runtime string

	mu       sync.Mutex
	active   int
	inflight int64    // bytes of uploads in progress, for the space check
	pending  []string // folders with new files since the last scan
	timer    *time.Timer
	scanning bool
	scanDir  string
	scanAt   time.Time
}

// NewFiles creates the manager. root and limits read the current settings.
func NewFiles(root func() string, limits func() Limits, p *player.Player, ev *events.Hub, log *slog.Logger, runtime string) *Files {
	return &Files{root: root, limits: limits, player: p, events: ev, log: log, runtime: runtime}
}

// Limits returns the current upload rules.
func (f *Files) Limits() Limits { return f.limits() }

// Run cleans leftover temporary files at start and every hour.
func (f *Files) Run(ctx context.Context) {
	f.cleanTemp()
	clean := time.NewTicker(time.Hour)
	defer clean.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-clean.C:
			f.cleanTemp()
		}
	}
}

// cleanTemp removes .part files older than an hour from the staging dir.
func (f *Files) cleanTemp() {
	dir := filepath.Join(f.root(), TempDir)
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

// resolve validates a relative path that must name something below the
// root, never the root itself.
func (f *Files) resolve(root, rel string) (string, string, *OpError) {
	clean, err := CleanRel(rel)
	if err != nil || clean == "" {
		return "", "", &OpError{Status: http.StatusUnprocessableEntity, Detail: "that path is not allowed"}
	}
	abs, err := Abs(root, clean)
	if err != nil {
		return "", "", &OpError{Status: http.StatusUnprocessableEntity, Detail: "that path is not allowed"}
	}
	return abs, clean, nil
}

// CheckResult answers the pre-upload check.
type CheckResult struct {
	Existing    []string `json:"existing" doc:"Paths that already exist"`
	Unsupported []string `json:"unsupported" doc:"Paths with an extension that is not allowed"`
	Invalid     []string `json:"invalid" doc:"Paths that are not allowed"`
	FreeBytes   int64    `json:"free_bytes" doc:"Space an upload can use after the reserve. 0 when unknown."`
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
		abs, _, oe := f.resolve(root, p)
		if oe != nil {
			res.Invalid = append(res.Invalid, p)
			continue
		}
		if !lim.Extension(p) {
			res.Unsupported = append(res.Unsupported, p)
			continue
		}
		if _, err := os.Lstat(abs); err == nil {
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
// root. The file is synced and then renamed into place, so MPD never sees
// a partial file.
func (f *Files) Upload(rel, conflict string, body io.Reader, size int64) (UploadResult, error) {
	lim := f.limits()
	root := f.root()
	abs, rel, oe := f.resolve(root, rel)
	if oe != nil {
		return UploadResult{}, oe
	}
	if !lim.Extension(rel) {
		return UploadResult{}, &OpError{Status: http.StatusUnsupportedMediaType, Detail: "that file type is not allowed"}
	}
	if size > lim.MaxBytes {
		return UploadResult{}, &OpError{Status: http.StatusRequestEntityTooLarge, Detail: fmt.Sprintf("the file is larger than the %d MB limit", lim.MaxBytes>>20)}
	}
	st := Stat(root)
	if st.Missing {
		return UploadResult{}, &OpError{Status: http.StatusNotFound, Detail: st.Problem}
	}
	if st.ReadOnly {
		return UploadResult{}, f.permissionError(root, root)
	}
	if conflict != "replace" {
		conflict = "skip"
	}
	if info, err := os.Lstat(abs); err == nil {
		if info.IsDir() {
			return UploadResult{}, &OpError{Status: http.StatusConflict, Detail: "a folder with that name exists"}
		}
		if conflict == "skip" {
			return UploadResult{Path: rel, Skipped: true}, nil
		}
	}
	// A folder upload keeps its structure. The nearest existing folder
	// must be writable; the missing folders are created after the body
	// arrived, so a rejected upload leaves no empty folder behind.
	destDir := filepath.Dir(abs)
	base := nearestExisting(destDir, root)
	// Stat follows a symlink: the root, or a folder in it, is often a
	// link to a mount.
	if info, err := os.Stat(base); err == nil && !info.IsDir() {
		return UploadResult{}, &OpError{Status: http.StatusConflict, Detail: "a file is in the way of the folder path"}
	}
	if err := f.ensureWritable(base, root); err != nil {
		return UploadResult{}, err
	}
	// The space check and the reservation are one step, so a parallel
	// batch cannot pass the same free figure three times.
	f.mu.Lock()
	if size > 0 && st.FreeBytes > 0 && size > st.FreeBytes-lim.Reserve-f.inflight {
		f.mu.Unlock()
		return UploadResult{}, &OpError{Status: http.StatusInsufficientStorage, Detail: "not enough free space on the music disk"}
	}
	f.active++
	f.inflight += max(size, 0)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.inflight -= max(size, 0)
		f.mu.Unlock()
		// The next space check must see the new file.
		ForgetStat()
	}()

	tmpDir := filepath.Join(root, TempDir)
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return UploadResult{}, f.permissionError(root, root)
	}
	tmp, n, err := writeTemp(tmpDir, body, lim.MaxBytes)
	if err != nil {
		return UploadResult{}, err
	}
	defer os.Remove(tmp)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return UploadResult{}, f.opError(err, base, root)
	}
	if err := os.Rename(tmp, abs); err != nil {
		if !isCrossDevice(err) {
			return UploadResult{}, f.opError(err, destDir, root)
		}
		// A nested mount: rename cannot cross it. The file is written again
		// as a hidden .part file in the destination folder and renamed
		// there, which keeps the rename atomic. A visible copy in progress
		// is what MPD must never index.
		if err := moveAcross(tmp, abs); err != nil {
			return UploadResult{}, f.opError(err, destDir, root)
		}
	}
	f.noteUploaded(path.Dir(rel))
	return UploadResult{Path: rel, Bytes: n}, nil
}

// writeTemp streams body into a new .part file, capped at limit, and
// syncs it. It returns the file's path and size.
func writeTemp(tmpDir string, body io.Reader, limit int64) (string, int64, error) {
	out, err := os.CreateTemp(tmpDir, "*.part")
	if err != nil {
		return "", 0, &OpError{Status: http.StatusInternalServerError, Detail: "cannot create a temporary file: " + err.Error()}
	}
	tmp := out.Name()
	// Other tools and users read the music too.
	out.Chmod(0o644)
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
		if errors.Is(err, syscall.ENOSPC) {
			return "", 0, &OpError{Status: http.StatusInsufficientStorage, Detail: "the music disk is full"}
		}
		return "", 0, &OpError{Status: http.StatusInternalServerError, Detail: "upload failed: " + err.Error()}
	}
	return tmp, n, nil
}

// moveAcross copies src to a hidden, uniquely named .part file next to
// dst and renames it into place.
func moveAcross(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.part")
	if err != nil {
		return err
	}
	part := out.Name()
	defer func() {
		if err != nil {
			os.Remove(part)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err = out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Rename(part, dst)
}

// noteUploaded records the folder and arms the scan for five seconds after
// the last upload.
func (f *Files) noteUploaded(folder string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = append(f.pending, folder)
	if f.timer == nil {
		f.timer = time.AfterFunc(5*time.Second, f.maybeScan)
	} else {
		f.timer.Reset(5 * time.Second)
	}
}

// maybeScan runs one MPD update on the deepest folder that covers every
// upload since the last scan. It waits while uploads are active or a scan
// runs, unless that scan is older than scanTimeout.
func (f *Files) maybeScan() {
	f.mu.Lock()
	if f.scanning && time.Since(f.scanAt) > scanTimeout {
		f.log.Warn("no end event for the library scan, giving up the wait", "folder", f.scanDir)
		f.scanning = false
	}
	if f.active > 0 || f.scanning || len(f.pending) == 0 {
		if len(f.pending) > 0 {
			f.timer.Reset(5 * time.Second)
		}
		f.mu.Unlock()
		return
	}
	folder := commonFolder(f.pending)
	f.pending = nil
	f.scanning, f.scanDir, f.scanAt = true, folder, time.Now()
	f.mu.Unlock()
	if _, err := f.player.Update(folder); err != nil {
		f.log.Warn("cannot start the library scan after uploads", "folder", folder, "error", err)
		f.mu.Lock()
		f.scanning = false
		f.mu.Unlock()
		f.events.Publish(events.Upload, folder)
	}
}

// ResetScan forgets a scan in progress after MPD restarted, because the
// end event of the old scan never comes. Pending folders are scanned
// again.
func (f *Files) ResetScan() {
	f.mu.Lock()
	if f.scanning {
		f.pending = append(f.pending, f.scanDir)
		f.scanning = false
	}
	more := len(f.pending) > 0
	f.mu.Unlock()
	if more {
		f.timer.Reset(5 * time.Second)
	}
}

// ScanPending reports whether a scan after uploads is in progress.
func (f *Files) ScanPending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scanning
}

// NoteUpdate is called with MPD's updating flag after an update event.
// When the scan that followed uploads is over, clients are told and the
// next batch can scan.
func (f *Files) NoteUpdate(updating bool) {
	if updating {
		return
	}
	f.mu.Lock()
	if !f.scanning {
		f.mu.Unlock()
		return
	}
	folder := f.scanDir
	f.scanning = false
	more := len(f.pending) > 0
	f.mu.Unlock()
	f.events.Publish(events.Upload, folder)
	if more {
		f.maybeScan()
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
	abs, rel, oe := f.resolve(root, rel)
	if oe != nil {
		return oe
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
	// MPD lists a folder only after a scan finds it.
	f.noteUploaded(rel)
	f.events.Publish(events.Library, path.Dir(rel))
	return nil
}

// nearestExisting walks up from dir to the first path that exists, and
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
	// The walk does not follow a symlinked root, and a root is often a
	// link to a mount.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
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
		// The command names the first bad folder. With many, the root
		// covers them all.
		target := filepath.Join(root, filepath.FromSlash(rep.Problems[0]))
		if len(rep.Problems) > 1 {
			target = root
		}
		rep.Fix, rep.Command = f.fixText(target)
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

func (f *Files) permissionError(dir, root string) *OpError {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		rel = ""
	}
	fix, cmd := f.fixText(dir)
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
	switch {
	case errors.Is(err, fs.ErrPermission):
		return f.permissionError(dir, root)
	case errors.Is(err, fs.ErrNotExist):
		return &OpError{Status: http.StatusNotFound, Detail: "no such folder"}
	case errors.Is(err, syscall.EISDIR), errors.Is(err, syscall.ENOTDIR), errors.Is(err, fs.ErrExist):
		return &OpError{Status: http.StatusConflict, Detail: "a file or folder is in the way: " + err.Error()}
	case errors.Is(err, syscall.ENOSPC):
		return &OpError{Status: http.StatusInsufficientStorage, Detail: "the music disk is full"}
	}
	return &OpError{Status: http.StatusInternalServerError, Detail: err.Error()}
}

// fixText explains how to repair ownership of dir. Under Docker the
// command runs on the host, on the directory that is mounted at dir, and
// the container has no way to know that host path.
func (f *Files) fixText(dir string) (fix, command string) {
	note := " Do not run a recursive chown on a NAS mount with its own UID mapping. Change the export or the mount options instead."
	if f.runtime == "docker" {
		return "Run this on the Docker host, not in the container, on the host directory that is mounted at " + dir +
				". The container user is UID 1000. The other option is to set user: in compose.yaml to the owner of the music directory." + note,
			"chown -R 1000:1000 <host directory mounted at " + dir + ">"
	}
	return "Give the folder to the service user." + note, "chown -R jukem:jukem " + dir
}
