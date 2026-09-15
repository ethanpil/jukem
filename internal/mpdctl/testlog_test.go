package mpdctl

import (
	"context"
	"log/slog"
)

// captureHandler collects log messages for assertions.
type captureHandler struct {
	lines *[]string
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.lines = append(*h.lines, r.Message)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }
