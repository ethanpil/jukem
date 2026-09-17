package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fhs/gompd/v2/mpd"

	"jukem/internal/events"
	"jukem/internal/mpdctl"
	"jukem/internal/player"
	"jukem/internal/watchdog"
)

// zoneInput is the optional zone parameter. One zone exists today; the
// parameter keeps a future multi-zone version from breaking clients.
type zoneInput struct {
	Zone string `query:"zone" default:"main" doc:"Playback zone; only main exists"`
}

// Resolve rejects every zone but main. huma runs it for each handler that
// embeds zoneInput.
func (z *zoneInput) Resolve(ctx huma.Context) []error {
	if z.Zone != "" && z.Zone != "main" {
		return []error{&huma.ErrorDetail{Message: "no such zone: " + z.Zone, Location: "query.zone", Value: z.Zone}}
	}
	return nil
}

// OutputInfo names the selected output and whether it is present.
type OutputInfo struct {
	Name    string `json:"name,omitempty"`
	Present bool   `json:"present"`
}

// StatusBody is the answer to GET /status.
type StatusBody struct {
	Player player.Status   `json:"player"`
	Owner  player.Owner    `json:"owner"`
	Output OutputInfo      `json:"output"`
	Health watchdog.Status `json:"health" enum:"ok,warning,error"`
	MPD    bool            `json:"mpd_running"`
}

type statusOutput struct {
	Body StatusBody
}

// mpdError maps a failed MPD command to a problem response. A protocol
// error about a missing song or a bad argument is the client's fault.
func mpdError(err error) error {
	if err == nil {
		return nil
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	if errors.Is(err, mpdctl.ErrNotRunning) {
		return huma.Error503ServiceUnavailable("MPD is not running")
	}
	var me mpd.Error
	if errors.As(err, &me) {
		switch me.Code {
		case 50: // ACK_ERROR_NO_EXIST
			return huma.Error404NotFound(me.Message)
		case 2: // ACK_ERROR_ARG
			return huma.Error422UnprocessableEntity(me.Message)
		}
	}
	return huma.Error502BadGateway("MPD rejected the command: " + err.Error())
}

func (s *Server) registerPlayer(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-status", Method: http.MethodGet, Path: "/status", Tags: []string{"player"},
		Summary: "Player state, current track, owner, output and health summary",
	}, func(ctx context.Context, in *zoneInput) (*statusOutput, error) {
		return &statusOutput{Body: s.status()}, nil
	})

	type actionInput struct {
		zoneInput
		Action string `path:"action" enum:"play,pause,stop,next,previous"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "player-action", Method: http.MethodPost, Path: "/player/{action}", Tags: []string{"player"},
		Summary: "Transport control", Description: "play, pause and stop create an override while the scheduler is on. Refetch GET /status for the result.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *actionInput) (*struct{}, error) {
		p, _ := PrincipalFrom(ctx)
		var err error
		switch in.Action {
		case "next":
			err = s.opts.Player.Next()
		case "previous":
			err = s.opts.Player.Previous()
		default:
			err = s.opts.Transport(ctx, in.Action, p.Source())
		}
		return nil, mpdError(err)
	})

	type volumeInput struct {
		zoneInput
		Body struct {
			Volume int `json:"volume" minimum:"0" maximum:"100"`
		}
	}
	type volumeOutput struct {
		Body struct {
			Volume int `json:"volume" doc:"The volume applied after the configured limits"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "set-volume", Method: http.MethodPut, Path: "/player/volume", Tags: []string{"player"},
		Summary: "Set the volume, clamped to the configured limits",
	}, func(ctx context.Context, in *volumeInput) (*volumeOutput, error) {
		v, err := s.opts.Player.SetVolume(in.Body.Volume)
		if err != nil {
			return nil, mpdError(err)
		}
		out := &volumeOutput{}
		out.Body.Volume = v
		return out, nil
	})

	type optionsInput struct {
		zoneInput
		Body struct {
			Shuffle *bool `json:"shuffle,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "set-options", Method: http.MethodPut, Path: "/player/options", Tags: []string{"player"},
		Summary: "Set play options", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *optionsInput) (*struct{}, error) {
		if in.Body.Shuffle != nil {
			set := s.opts.Player.SetShuffle
			if s.opts.SetShuffle != nil {
				set = func(on bool) error { return s.opts.SetShuffle(ctx, on) }
			}
			if err := set(*in.Body.Shuffle); err != nil {
				return nil, mpdError(err)
			}
			// MPD sends no event, because its own options did not change.
			if s.opts.Events != nil {
				s.opts.Events.Publish(events.Player, "")
			}
		}
		return nil, nil
	})

	type seekInput struct {
		zoneInput
		Body struct {
			Position float64 `json:"position" minimum:"0" doc:"Seconds from the start of the current track"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "seek", Method: http.MethodPost, Path: "/player/seek", Tags: []string{"player"},
		Summary: "Seek inside the current track", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *seekInput) (*struct{}, error) {
		return nil, mpdError(s.opts.Player.Seek(in.Body.Position))
	})

	type queueInput struct {
		zoneInput
		Offset int `query:"offset" default:"0" minimum:"0"`
		Limit  int `query:"limit" default:"200" minimum:"1" maximum:"1000"`
	}
	type queueOutput struct {
		Body struct {
			Tracks []player.Track `json:"tracks"`
			Total  int            `json:"total"`
			Offset int            `json:"offset"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-queue", Method: http.MethodGet, Path: "/queue", Tags: []string{"queue"},
		Summary: "One page of the queue",
	}, func(ctx context.Context, in *queueInput) (*queueOutput, error) {
		tracks, total, err := s.opts.Player.Queue(in.Offset, in.Limit)
		if err != nil {
			return nil, mpdError(err)
		}
		out := &queueOutput{}
		out.Body.Tracks, out.Body.Total, out.Body.Offset = tracks, total, in.Offset
		return out, nil
	})

	type queueActionInput struct {
		zoneInput
		Body QueueAction
	}
	huma.Register(api, huma.Operation{
		OperationID: "queue-action", Method: http.MethodPost, Path: "/queue", Tags: []string{"queue"},
		Summary: "Play Now, Play Next or Add to Queue for files, a folder or a playlist",
	}, func(ctx context.Context, in *queueActionInput) (*queueActionOutput, error) {
		p, _ := PrincipalFrom(ctx)
		res, err := s.opts.QueueAction(ctx, in.Body, p.Source())
		if err != nil {
			return nil, mpdError(err)
		}
		return &queueActionOutput{Body: res}, nil
	})

	type playIDInput struct {
		zoneInput
		Body struct {
			ID int `json:"id" doc:"Queue entry id"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "play-queue-entry", Method: http.MethodPost, Path: "/queue/play", Tags: []string{"queue"},
		Summary: "Play from a queue entry", Description: "Creates an override while the scheduler is on, like play.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *playIDInput) (*struct{}, error) {
		p, _ := PrincipalFrom(ctx)
		return nil, mpdError(s.opts.PlayEntry(ctx, in.Body.ID, p.Source()))
	})

	type queueIDInput struct {
		zoneInput
		ID int `path:"id"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "remove-queue-entry", Method: http.MethodDelete, Path: "/queue/{id}", Tags: []string{"queue"},
		Summary: "Remove a queue entry", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *queueIDInput) (*struct{}, error) {
		return nil, mpdError(s.opts.Player.Remove(in.ID))
	})

	type moveInput struct {
		zoneInput
		Body struct {
			ID int `json:"id" doc:"Queue entry id"`
			To int `json:"to" minimum:"0" doc:"New position"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "move-queue-entry", Method: http.MethodPost, Path: "/queue/move", Tags: []string{"queue"},
		Summary: "Reorder the queue", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *moveInput) (*struct{}, error) {
		return nil, mpdError(s.opts.Player.Move(in.Body.ID, in.Body.To))
	})
}

// QueueAction is the body of POST /queue. Folder "/" is the whole library.
type QueueAction struct {
	Action   string   `json:"action" enum:"play_now,play_next,add"`
	Files    []string `json:"files,omitempty" doc:"Paths relative to the music root, in the order shown"`
	Folder   string   `json:"folder,omitempty" doc:"A folder; every track below it in path order. / is the whole library"`
	Playlist int64    `json:"playlist,omitempty" doc:"A playlist id"`
}

// QueueResult reports what a queue action did.
type QueueResult struct {
	Added     int  `json:"added" doc:"Tracks added"`
	Truncated bool `json:"truncated" doc:"True when the source held more than the queue ceiling"`
	Shuffle   bool `json:"shuffle" doc:"True when the queue plays in a shuffled order"`
}

type queueActionOutput struct {
	Body QueueResult
}

// status assembles the status body.
func (s *Server) status() StatusBody {
	st, err := s.opts.Player.Status()
	body := StatusBody{Player: st, Owner: s.opts.Owner(), MPD: err == nil}
	if err != nil {
		body.Player = player.Status{State: "stop"}
	}
	snap := s.opts.Devices.Snapshot()
	if snap.Saved != nil {
		body.Output.Name = snap.Saved.Name
	}
	body.Output.Present = snap.Selected != nil
	body.Health = s.opts.Health().Status
	return body
}
