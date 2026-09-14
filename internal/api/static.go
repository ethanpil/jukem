package api

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// staticFile is one embedded asset, compressed once at startup.
type staticFile struct {
	raw         []byte
	gz          []byte
	contentType string
}

// staticHandler serves the embedded web UI. Assets are served gzipped with a
// long cache when the request carries a version query string; index.html is
// never cached because it is the entry point that names the current version.
type staticHandler struct {
	files   map[string]*staticFile
	index   *staticFile
	version string
}

func newStaticHandler(fsys fs.FS, version string) (*staticHandler, error) {
	h := &staticHandler{files: map[string]*staticFile{}, version: version}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".keep") {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		f := &staticFile{raw: data, contentType: contentTypeFor(p)}
		if compressible(f.contentType) {
			var buf bytes.Buffer
			w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			w.Write(data)
			w.Close()
			f.gz = buf.Bytes()
		}
		if p == "index.html" {
			h.index = f
		} else {
			h.files["/"+p] = f
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return h, nil
}

func contentTypeFor(p string) string {
	switch path.Ext(p) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json", ".map":
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
		if r.URL.Query().Get("v") == h.version {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		serveStatic(w, r, f)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(path.Base(r.URL.Path), ".") {
		http.NotFound(w, r)
		return
	}
	// Every other path is the app shell; the hash router takes over.
	w.Header().Set("Cache-Control", "no-store")
	serveStatic(w, r, h.index)
}

func serveStatic(w http.ResponseWriter, r *http.Request, f *staticFile) {
	w.Header().Set("Content-Type", f.contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	body := f.raw
	if f.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		body = f.gz
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Last-Modified", startTime.UTC().Format(http.TimeFormat))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Write(body)
}

var startTime = time.Now()
