package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"syscall"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/library"
	"jukem/internal/store"
)

type alertsOutput struct {
	Body struct {
		Alerts []store.Alert `json:"alerts"`
	}
}

type historyOutput struct {
	Body struct {
		Rows []store.HistoryRow `json:"rows"`
	}
}

type snapshotOutput struct {
	Body struct {
		Path string `json:"path"`
	}
}

// registerSystemOps adds the alerts, history, maintenance and TLS
// endpoints that need the running application.
func (s *Server) registerSystemOps(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-alerts", Method: http.MethodGet, Path: "/alerts", Tags: []string{"system"},
		Summary: "Active alerts",
	}, func(ctx context.Context, _ *struct{}) (*alertsOutput, error) {
		list, err := s.store.ActiveAlerts(ctx)
		if err != nil {
			return nil, err
		}
		out := &alertsOutput{}
		out.Body.Alerts = list
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "dismiss-alert", Method: http.MethodDelete, Path: "/alerts/{id}", Tags: []string{"system"},
		Summary: "Dismiss an alert", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID int64 `path:"id"`
	}) (*struct{}, error) {
		ok, err := s.opts.Alerter.Dismiss(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound("no such active alert")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "test-alert", Method: http.MethodPost, Path: "/alerts/test", Tags: []string{"system"},
		Summary: "Send a test alert to the webhook", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		if err := s.opts.Alerter.Test(ctx); err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-history", Method: http.MethodGet, Path: "/history", Tags: []string{"system"},
		Summary: "Play history, newest first",
	}, func(ctx context.Context, in *struct {
		BeforeID int64 `query:"before_id" doc:"Return rows with a smaller id, for paging: the last id of the previous page"`
		Limit    int   `query:"limit" default:"100" minimum:"1" maximum:"500"`
	}) (*historyOutput, error) {
		rows, err := s.store.ListHistory(ctx, in.BeforeID, in.Limit)
		if err != nil {
			return nil, err
		}
		out := &historyOutput{}
		out.Body.Rows = rows
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-directories", Method: http.MethodGet, Path: "/system/directories", Tags: []string{"system"},
		Summary: "Server-side directory listing, for choosing the music root",
	}, func(ctx context.Context, in *struct {
		Path string `query:"path" default:"/"`
	}) (*struct{ Body library.DirListing }, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		d, err := library.ListDirs(in.Path)
		switch {
		case errors.Is(err, os.ErrInvalid):
			return nil, huma.Error422UnprocessableEntity("path must be absolute")
		case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			return nil, huma.Error404NotFound("no such directory")
		case errors.Is(err, os.ErrPermission):
			return nil, huma.Error403Forbidden("the jukem user cannot read " + in.Path)
		case err != nil:
			return nil, err
		}
		return &struct{ Body library.DirListing }{Body: d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "snapshot-database", Method: http.MethodPost, Path: "/system/snapshot", Tags: []string{"system"},
		Summary: "Copy the database to the snapshots directory",
	}, func(ctx context.Context, _ *struct{}) (*snapshotOutput, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		path, err := s.opts.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		out := &snapshotOutput{}
		out.Body.Path = path
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "restart-service", Method: http.MethodPost, Path: "/system/restart", Tags: []string{"system"},
		Summary: "Stop the service; the supervisor starts it again", DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		if err := s.opts.Restart(); err != nil {
			return nil, huma.Error503ServiceUnavailable(err.Error())
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "put-tls", Method: http.MethodPut, Path: "/settings/tls", Tags: []string{"settings"},
		Summary: "Store a certificate and key in PEM form", DefaultStatus: http.StatusNoContent,
		Description: "A running TLS listener uses the new pair at the next connection. The HTTPS switch itself applies at the next restart.",
	}, func(ctx context.Context, in *struct {
		Body struct {
			Certificate string `json:"certificate" minLength:"1"`
			Key         string `json:"key" minLength:"1"`
		}
	}) (*struct{}, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		if err := s.opts.StoreTLS(in.Body.Certificate, in.Body.Key); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "self-signed-tls", Method: http.MethodPost, Path: "/settings/tls/self-signed", Tags: []string{"settings"},
		Summary: "Generate and store a self-signed certificate", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		return nil, s.opts.SelfSignedTLS()
	})
}
