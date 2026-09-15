package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/store"
)

type settingsOutput struct {
	Body store.Settings
}

func (s *Server) registerSettings(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-settings", Method: http.MethodGet, Path: "/settings", Tags: []string{"settings"},
		Summary: "Every setting the UI exposes",
	}, func(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
		return &settingsOutput{Body: s.opts.Settings()}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "put-settings", Method: http.MethodPut, Path: "/settings", Tags: []string{"settings"},
		Summary: "Replace the settings", Description: "Send the whole object from GET /settings with the changed fields. Web session only: a leaked API key must not change where the music comes from.",
	}, func(ctx context.Context, in *struct{ Body store.Settings }) (*settingsOutput, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		if err := s.opts.UpdateSettings(ctx, in.Body); err != nil {
			return nil, err
		}
		return &settingsOutput{Body: s.opts.Settings()}, nil
	})
}
