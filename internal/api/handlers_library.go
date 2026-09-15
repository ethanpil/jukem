package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/library"
)

const badPathDetail = "that path is not allowed"

// libraryError maps a path error to 422. Other errors go to mpdError.
func libraryError(err error) error {
	if errors.Is(err, library.ErrBadPath) {
		return huma.Error422UnprocessableEntity(badPathDetail)
	}
	return mpdError(err)
}

// audioTypes maps the allowed extensions to media types. The Alpine
// runtime image has no system mime table.
var audioTypes = map[string]string{
	".mp3": "audio/mpeg", ".flac": "audio/flac", ".ogg": "audio/ogg", ".opus": "audio/ogg",
	".m4a": "audio/mp4", ".aac": "audio/aac", ".wav": "audio/wav", ".aiff": "audio/aiff", ".aif": "audio/aiff",
}

func (s *Server) registerLibrary(api huma.API, apiMux *http.ServeMux) {
	type browseInput struct {
		Path string `query:"path" doc:"Folder relative to the music root; empty for the root"`
		Page int    `query:"page" default:"0" minimum:"0"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "browse-library", Method: http.MethodGet, Path: "/library/browse", Tags: []string{"library"},
		Summary: "Folders and tracks, paged",
	}, func(ctx context.Context, in *browseInput) (*struct{ Body library.Page }, error) {
		page, err := s.opts.Library.Browse(in.Path, in.Page)
		if err != nil {
			return nil, libraryError(err)
		}
		return &struct{ Body library.Page }{Body: page}, nil
	})

	type searchInput struct {
		Q string `query:"q" minLength:"1" maxLength:"200"`
	}
	type searchOutput struct {
		Body struct {
			Entries []library.Entry `json:"entries"`
			Limited bool            `json:"limited" doc:"True when more tracks matched than were returned"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "search-library", Method: http.MethodGet, Path: "/library/search", Tags: []string{"library"},
		Summary: "Tag and file name search",
	}, func(ctx context.Context, in *searchInput) (*searchOutput, error) {
		entries, limited, err := s.opts.Library.Search(in.Q)
		if err != nil {
			return nil, mpdError(err)
		}
		out := &searchOutput{}
		out.Body.Entries = entries
		out.Body.Limited = limited
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "library-storage", Method: http.MethodGet, Path: "/library/storage", Tags: []string{"library"},
		Summary: "Space and read-only status of the music root",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body library.Storage }, error) {
		return &struct{ Body library.Storage }{Body: library.Stat(s.opts.Settings().MusicRoot)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "rescan-library", Method: http.MethodPost, Path: "/library/rescan", Tags: []string{"library"},
		Summary: "Full rescan of the music root", DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		_, err := s.opts.Player.Update("")
		return nil, mpdError(err)
	})

	// The preview streams a file with Range support for the browser's audio
	// element. An audio element cannot send a bearer header. Thus this is a
	// cookie-session endpoint.
	apiMux.HandleFunc("GET "+apiPrefix+"/library/preview", s.preview)
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "preview-track", Method: http.MethodGet, Path: "/library/preview", Tags: []string{"library"},
		Summary:     "Audio stream for browser preview, with Range support",
		Description: "Web session only: an audio element cannot send a bearer header.",
		Security:    []map[string][]string{{"session": {}}},
		Parameters:  []*huma.Param{{Name: "path", In: "query", Required: true, Schema: &huma.Schema{Type: "string"}}},
		Responses:   map[string]*huma.Response{"200": {Description: "The audio file"}, "206": {Description: "A byte range of the file"}},
	})
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok || p.Kind != KindSession {
		writeProblem(w, http.StatusForbidden, "the preview needs a web session")
		return
	}
	real, err := library.Resolve(s.opts.Settings().MusicRoot, r.URL.Query().Get("path"))
	switch {
	case errors.Is(err, library.ErrBadPath):
		writeProblem(w, http.StatusUnprocessableEntity, badPathDetail)
		return
	case err != nil:
		writeProblem(w, http.StatusNotFound, "no such file")
		return
	}
	f, err := os.Open(real)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "no such file")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeProblem(w, http.StatusNotFound, "no such file")
		return
	}
	ct := audioTypes[strings.ToLower(filepath.Ext(real))]
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	// A replaced upload must play the new audio, so the browser revalidates.
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}
