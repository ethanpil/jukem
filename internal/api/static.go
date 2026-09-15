package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// staticFile is one embedded asset. The gzip form is built on first use.
type staticFile struct {
	raw         []byte
	contentType string
	etag        string
	once        sync.Once
	gz          []byte
}

func (f *staticFile) gzipped() []byte {
	f.once.Do(func() {
		if !compressible(f.contentType) {
			return
		}
		var buf bytes.Buffer
		w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		w.Write(f.raw)
		w.Close()
		f.gz = buf.Bytes()
	})
	return f.gz
}

// staticHandler serves the embedded web UI. A known asset is served gzipped,
// with a long cache when the request carries the current version query
// string. Every other path gets the shell page with shellStatus, and the
// shell is never cached because it names the current version.
type staticHandler struct {
	files       map[string]*staticFile
	shell       *staticFile
	shellStatus int
	versionQ    string
}

var lastModified = time.Now().UTC().Format(http.TimeFormat)

func newStaticHandler(fsys fs.FS, version, shell string, shellStatus int) (*staticHandler, error) {
	h := &staticHandler{files: map[string]*staticFile{}, shellStatus: shellStatus, versionQ: "v=" + version}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		h.files["/"+p] = &staticFile{raw: data, contentType: contentTypeFor(p), etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.shell = h.files["/"+shell]
	if h.shell == nil {
		return nil, &fs.PathError{Op: "open", Path: shell, Err: fs.ErrNotExist}
	}
	// The shell pages are reachable only as the fallback.
	delete(h.files, "/index.html")
	delete(h.files, "/maintenance.html")
	return h, nil
}

func contentTypeFor(p string) string {
	// The stdlib table covers the common types. Font types are missing from
	// it, and /etc/mime.types on the host can change .js, so these stay fixed.
	switch path.Ext(p) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".map":
		return "application/json"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	}
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func compressible(ct string) bool {
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "json") || strings.Contains(ct, "svg")
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if f, ok := h.files[r.URL.Path]; ok {
		if r.URL.RawQuery == h.versionQ {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		serveStatic(w, r, f, http.StatusOK)
		return
	}
	// The hash router owns every other path, so each one gets the shell.
	w.Header().Set("Cache-Control", "no-store")
	serveStatic(w, r, h.shell, h.shellStatus)
}

func serveStatic(w http.ResponseWriter, r *http.Request, f *staticFile, status int) {
	if strings.HasPrefix(f.contentType, "image/svg+xml") {
		// The logo changes its colour with an inline style for dark mode.
		// An SVG cannot load anything else, and a browser shown the SVG
		// alone runs no script from it. A 304 answer carries the same
		// policy, because the browser keeps the headers of the last answer.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	}
	// The tag is the content hash, so a browser's revalidation costs no
	// bytes after a restart either.
	w.Header().Set("ETag", f.etag)
	if status == http.StatusOK && strings.Contains(r.Header.Get("If-None-Match"), f.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", f.contentType)
	body := f.raw
	if gz := f.gzipped(); gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		body = gz
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Last-Modified", lastModified)
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}
