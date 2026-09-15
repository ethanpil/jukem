package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/library"
)

// problemFromOp turns a file operation error into the problem document
// every endpoint uses. The fix and the command travel in errors[0], with
// the folder as the location.
func problemFromOp(oe *library.OpError) *huma.ErrorModel {
	e := &huma.ErrorModel{Status: oe.Status, Title: http.StatusText(oe.Status), Detail: oe.Detail}
	if oe.Fix != "" {
		e.Errors = []*huma.ErrorDetail{{Message: oe.Fix, Location: "folder:" + oe.Folder, Value: oe.Command}}
	}
	return e
}

// opError maps a file operation error for a huma handler.
func opError(err error) error {
	if err == nil {
		return nil
	}
	var oe *library.OpError
	if errors.As(err, &oe) {
		return problemFromOp(oe)
	}
	return libraryError(err)
}

func (s *Server) registerUpload(api huma.API, apiMux *http.ServeMux) {
	type checkInput struct {
		Body struct {
			Paths []string `json:"paths" maxItems:"2000"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "check-files", Method: http.MethodPost, Path: "/library/files/check", Tags: []string{"library"},
		Summary: "Which of the given relative paths already exist or are not allowed", Description: "Send at most 2000 paths per call.",
	}, func(ctx context.Context, in *checkInput) (*struct{ Body library.CheckResult }, error) {
		return &struct{ Body library.CheckResult }{Body: s.opts.Files.Check(in.Body.Paths)}, nil
	})

	type folderInput struct {
		Body struct {
			Path string `json:"path" minLength:"1" doc:"Folder to create, relative to the music root"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-folder", Method: http.MethodPost, Path: "/library/folders", Tags: []string{"library"},
		Summary: "Create a folder", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *folderInput) (*struct{}, error) {
		return nil, opError(s.opts.Files.NewFolder(in.Body.Path))
	})

	huma.Register(api, huma.Operation{
		OperationID: "check-permissions", Method: http.MethodPost, Path: "/library/permissions/check", Tags: []string{"library"},
		Summary: "Crawl the tree and report folders that are not writable",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body library.PermissionReport }, error) {
		rep, err := s.opts.Files.CheckPermissions(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("permission check failed: " + err.Error())
		}
		return &struct{ Body library.PermissionReport }{Body: rep}, nil
	})

	// The upload takes the raw body, so it stays outside huma.
	apiMux.HandleFunc("PUT "+apiPrefix+"/library/files", s.upload)
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "upload-file", Method: http.MethodPut, Path: "/library/files", Tags: []string{"library"},
		Summary:     "Upload one file as the raw body",
		Description: "The path must stay inside the music root, the extension must be allowed, and the disk must have room. conflict is skip (default) or replace.",
		Parameters: []*huma.Param{
			{Name: "path", In: "query", Required: true, Schema: &huma.Schema{Type: "string"}},
			{Name: "conflict", In: "query", Schema: &huma.Schema{Type: "string", Enum: []any{"skip", "replace"}}},
		},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/octet-stream": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}},
		Responses:   map[string]*huma.Response{"201": {Description: "Stored"}, "200": {Description: "Skipped, the file existed"}},
	})
}

// upload streams one file into the library.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	lim := s.opts.Files.Limits()
	// A body that stops arriving must end, or the upload counts as active
	// and holds every library scan. Half an hour covers a slow WiFi link.
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Minute))
	// The reader is capped regardless of Content-Length, so a chunked
	// body cannot exceed the limit either.
	body := http.MaxBytesReader(w, r.Body, lim.MaxBytes+1)
	res, err := s.opts.Files.Upload(r.URL.Query().Get("path"), r.URL.Query().Get("conflict"), body, r.ContentLength)
	if err != nil {
		var oe *library.OpError
		if errors.As(err, &oe) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(oe.Status)
			json.NewEncoder(w).Encode(problemFromOp(oe))
			return
		}
		writeProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if res.Skipped {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	json.NewEncoder(w).Encode(res)
}
