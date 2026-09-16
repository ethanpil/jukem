package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/events"
	"jukem/internal/library"
	"jukem/internal/store"
)

// validateAnnouncement checks the times and the source of an announcement
// and returns it with a clean source reference.
func validateAnnouncement(a store.Announcement) (store.Announcement, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return a, huma.Error422UnprocessableEntity("the announcement needs a name")
	}
	switch a.Mode {
	case "at":
		if a.AtTime == nil {
			return a, huma.Error422UnprocessableEntity("mode at needs at_time")
		}
		a.StartTime, a.EndTime, a.EveryMinutes = nil, nil, nil
	case "every":
		if a.StartTime == nil || a.EndTime == nil || a.EveryMinutes == nil {
			return a, huma.Error422UnprocessableEntity("mode every needs start_time, end_time and every_minutes")
		}
		if *a.EveryMinutes < 1 {
			return a, huma.Error422UnprocessableEntity("every_minutes must be 1 or more")
		}
		if *a.EndTime <= *a.StartTime {
			return a, huma.Error422UnprocessableEntity("the end must be after the start")
		}
		a.AtTime = nil
	default:
		return a, huma.Error422UnprocessableEntity("mode must be at or every")
	}
	switch a.SourceKind {
	case "file", "random", "cycle":
	default:
		return a, huma.Error422UnprocessableEntity("source_kind must be file, random or cycle")
	}
	clean, err := library.CleanRel(a.SourceRef)
	if err != nil {
		return a, huma.Error422UnprocessableEntity("the source path is not allowed")
	}
	if clean == "" {
		if a.SourceKind == "file" {
			return a, huma.Error422UnprocessableEntity("choose the file to play")
		}
		// An empty folder reference is the whole library, which is not an
		// announcement.
		return a, huma.Error422UnprocessableEntity("choose the folder to play from")
	}
	a.SourceRef = clean
	return a, nil
}

// announcementsChanged tells the clients that the announcements changed.
func (s *Server) announcementsChanged() {
	if s.opts.Events != nil {
		s.opts.Events.Publish(events.Schedule, "")
	}
}

func (s *Server) registerAnnouncements(api huma.API) {
	type idInput struct {
		ID int64 `path:"id"`
	}
	type listOutput struct {
		Body struct {
			Announcements []store.Announcement `json:"announcements"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-announcements", Method: http.MethodGet, Path: "/announcements", Tags: []string{"schedule"},
		Summary: "Announcements", Description: "Files that play instead of the music at their own times.",
	}, func(ctx context.Context, _ *struct{}) (*listOutput, error) {
		list, err := s.store.ListAnnouncements(ctx)
		if err != nil {
			return nil, err
		}
		out := &listOutput{}
		out.Body.Announcements = list
		return out, nil
	})

	type body struct {
		Body store.Announcement
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-announcement", Method: http.MethodPost, Path: "/announcements", Tags: []string{"schedule"},
		Summary: "Create an announcement", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *body) (*body, error) {
		a, err := validateAnnouncement(in.Body)
		if err != nil {
			return nil, err
		}
		a.ID = 0
		a, err = s.store.CreateAnnouncement(ctx, a)
		if err != nil {
			return nil, err
		}
		s.announcementsChanged()
		return &body{Body: a}, nil
	})

	type updateInput struct {
		ID   int64 `path:"id"`
		Body store.Announcement
	}
	huma.Register(api, huma.Operation{
		OperationID: "update-announcement", Method: http.MethodPut, Path: "/announcements/{id}", Tags: []string{"schedule"},
		Summary: "Replace an announcement",
	}, func(ctx context.Context, in *updateInput) (*body, error) {
		a, err := validateAnnouncement(in.Body)
		if err != nil {
			return nil, err
		}
		a.ID = in.ID
		if _, ok, err := s.store.GetAnnouncement(ctx, in.ID); err != nil {
			return nil, err
		} else if !ok {
			return nil, huma.Error404NotFound("no such announcement")
		}
		if err := s.store.UpdateAnnouncement(ctx, a); err != nil {
			return nil, err
		}
		s.announcementsChanged()
		return &body{Body: a}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-announcement", Method: http.MethodDelete, Path: "/announcements/{id}", Tags: []string{"schedule"},
		Summary: "Delete an announcement", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if _, ok, err := s.store.GetAnnouncement(ctx, in.ID); err != nil {
			return nil, err
		} else if !ok {
			return nil, huma.Error404NotFound("no such announcement")
		}
		if err := s.store.DeleteAnnouncement(ctx, in.ID); err != nil {
			return nil, err
		}
		s.announcementsChanged()
		return nil, nil
	})

	if s.opts.PlayAnnouncement == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "play-announcement", Method: http.MethodPost, Path: "/announcements/{id}/play", Tags: []string{"schedule"},
		Summary: "Play an announcement now", DefaultStatus: http.StatusAccepted,
		Description: "Starts the announcement, to hear it. The music continues afterwards at the point it stopped. " +
			"The answer comes before the announcement ends, and the play time of the schedule is not changed.",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		a, ok, err := s.store.GetAnnouncement(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound("no such announcement")
		}
		// The call answers when the announcement starts. A request that
		// waited for the end would hold a connection for minutes, and a
		// browser that goes away would stop the music in the middle.
		if err := s.opts.PlayAnnouncement(ctx, a, time.Time{}); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return nil, nil
	})
}
