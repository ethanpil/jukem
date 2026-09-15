package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/library"
	"jukem/internal/store"
)

// PlaylistView is a playlist with its entries.
type PlaylistView struct {
	store.Playlist
	Entries []library.PlaylistEntry `json:"entries"`
	Missing int                     `json:"missing" doc:"Entries whose file is gone"`
}

// PlaylistSummary is a playlist in the list.
type PlaylistSummary struct {
	store.Playlist
	Count int `json:"count"`
}

func (s *Server) playlist(ctx context.Context, id int64) (store.Playlist, error) {
	pl, ok, err := s.store.GetPlaylist(ctx, id)
	if err != nil {
		return pl, err
	}
	if !ok {
		return pl, huma.Error404NotFound("no such playlist")
	}
	return pl, nil
}

func (s *Server) playlistView(pl store.Playlist) (*struct{ Body PlaylistView }, error) {
	entries, err := s.opts.Playlists.EntriesWithState(pl.Name)
	if err != nil {
		return nil, err
	}
	v := PlaylistView{Playlist: pl, Entries: entries}
	for _, e := range entries {
		if e.Missing {
			v.Missing++
		}
	}
	return &struct{ Body PlaylistView }{Body: v}, nil
}

func (s *Server) registerPlaylists(api huma.API) {
	type idInput struct {
		ID int64 `path:"id"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-playlists", Method: http.MethodGet, Path: "/playlists", Tags: []string{"playlists"},
		Summary: "List playlists",
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Playlists []PlaylistSummary `json:"playlists"`
		}
	}, error) {
		lists, err := s.store.ListPlaylists(ctx)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Playlists []PlaylistSummary `json:"playlists"`
			}
		}{}
		out.Body.Playlists = []PlaylistSummary{}
		for _, pl := range lists {
			entries, _ := s.opts.Playlists.Entries(pl.Name)
			out.Body.Playlists = append(out.Body.Playlists, PlaylistSummary{Playlist: pl, Count: len(entries)})
		}
		return out, nil
	})

	type createInput struct {
		Body struct {
			Name  string   `json:"name" minLength:"1" maxLength:"100"`
			Files []string `json:"files,omitempty" doc:"Initial entries"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-playlist", Method: http.MethodPost, Path: "/playlists", Tags: []string{"playlists"},
		Summary: "Create a playlist", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createInput) (*struct{ Body PlaylistView }, error) {
		pl, err := s.opts.Playlists.Create(ctx, in.Body.Name)
		if err != nil {
			return nil, opError(err)
		}
		if len(in.Body.Files) > 0 {
			if err := s.opts.Playlists.Set(ctx, pl, in.Body.Files); err != nil {
				return nil, opError(err)
			}
		}
		return s.playlistView(pl)
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-playlist", Method: http.MethodGet, Path: "/playlists/{id}", Tags: []string{"playlists"},
		Summary: "A playlist with its entries and missing-file flags",
	}, func(ctx context.Context, in *idInput) (*struct{ Body PlaylistView }, error) {
		pl, err := s.playlist(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return s.playlistView(pl)
	})

	type updateInput struct {
		ID   int64 `path:"id"`
		Body struct {
			Name    *string   `json:"name,omitempty" minLength:"1" maxLength:"100" doc:"New name"`
			Entries *[]string `json:"entries,omitempty" doc:"The complete new entry list, for reorder or removal"`
			Append  []string  `json:"append,omitempty" doc:"Files to add at the end"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "update-playlist", Method: http.MethodPut, Path: "/playlists/{id}", Tags: []string{"playlists"},
		Summary: "Rename, replace the entries, or append tracks",
	}, func(ctx context.Context, in *updateInput) (*struct{ Body PlaylistView }, error) {
		pl, err := s.playlist(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if in.Body.Name != nil {
			if err := s.opts.Playlists.Rename(ctx, pl, *in.Body.Name); err != nil {
				return nil, opError(err)
			}
			pl.Name = *in.Body.Name
		}
		if in.Body.Entries != nil {
			if err := s.opts.Playlists.Set(ctx, pl, *in.Body.Entries); err != nil {
				return nil, opError(err)
			}
		}
		if len(in.Body.Append) > 0 {
			if _, err := s.opts.Playlists.Append(ctx, pl, in.Body.Append); err != nil {
				return nil, opError(err)
			}
		}
		pl, _ = s.playlist(ctx, in.ID)
		return s.playlistView(pl)
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-playlist", Method: http.MethodDelete, Path: "/playlists/{id}", Tags: []string{"playlists"},
		Summary: "Delete a playlist", Description: "Rules and exceptions that use it are deleted too.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		pl, err := s.playlist(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return nil, opError(s.opts.Playlists.Delete(ctx, pl))
	})
}

func (s *Server) registerFileOps(api huma.API) {
	type pathsInput struct {
		Body struct {
			Paths []string `json:"paths" minItems:"1" maxItems:"2000"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "inspect-paths", Method: http.MethodPost, Path: "/library/inspect", Tags: []string{"library"},
		Summary: "Count what the paths hold and which playlists and schedules refer to them",
	}, func(ctx context.Context, in *pathsInput) (*struct{ Body library.Inspection }, error) {
		ins, err := s.opts.Files.Inspect(ctx, s.refs(), in.Body.Paths)
		if err != nil {
			return nil, opError(err)
		}
		return &struct{ Body library.Inspection }{Body: ins}, nil
	})

	type moveInput struct {
		Body struct {
			From string `json:"from" minLength:"1"`
			To   string `json:"to" minLength:"1" doc:"The new relative path, including the name"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "move-path", Method: http.MethodPost, Path: "/library/move", Tags: []string{"library"},
		Summary: "Move or rename, updating playlist and schedule references", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *moveInput) (*struct{}, error) {
		return nil, opError(s.opts.Files.Move(ctx, s.refs(), in.Body.From, in.Body.To))
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-paths", Method: http.MethodPost, Path: "/library/delete", Tags: []string{"library"},
		Summary: "Delete files and folders for good", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *pathsInput) (*struct{}, error) {
		return nil, opError(s.opts.Files.Delete(ctx, s.refs(), in.Body.Paths))
	})

	type dnpInput struct {
		Body struct {
			File  string `json:"file" minLength:"1"`
			Title string `json:"title,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-do-not-play", Method: http.MethodGet, Path: "/do-not-play", Tags: []string{"library"},
		Summary: "Tracks kept out of scheduled playback",
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Entries []store.DoNotPlayEntry `json:"entries"`
		}
	}, error) {
		list, err := s.store.ListDoNotPlay(ctx)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Entries []store.DoNotPlayEntry `json:"entries"`
			}
		}{}
		out.Body.Entries = list
		return out, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "add-do-not-play", Method: http.MethodPost, Path: "/do-not-play", Tags: []string{"library"},
		Summary: "Keep a track out of all future scheduled playback", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *dnpInput) (*struct{}, error) {
		file, err := library.CleanRel(in.Body.File)
		if err != nil || file == "" {
			return nil, huma.Error422UnprocessableEntity(badPathDetail)
		}
		return nil, s.store.AddDoNotPlay(ctx, file, in.Body.Title)
	})
	huma.Register(api, huma.Operation{
		OperationID: "remove-do-not-play", Method: http.MethodDelete, Path: "/do-not-play", Tags: []string{"library"},
		Summary: "Allow a track again", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		File string `query:"file" minLength:"1"`
	}) (*struct{}, error) {
		ok, err := s.store.RemoveDoNotPlay(ctx, in.File)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound("that track is not on the list")
		}
		return nil, nil
	})
}

func (s *Server) refs() library.References {
	return library.References{Playlists: s.opts.Playlists, Store: s.store}
}
