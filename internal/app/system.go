package app

import (
	"context"
	"errors"
	"path/filepath"
)

// Snapshot copies the database on request.
func (a *App) Snapshot(ctx context.Context) (string, error) {
	v, err := a.Store.Version(ctx)
	if err != nil {
		return "", err
	}
	return a.Store.Snapshot(ctx, filepath.Join(a.cfg.DataDir, "snapshots"), v)
}

// RequestRestart stops the service cleanly. The supervisor, or Docker,
// starts it again.
func (a *App) RequestRestart() error {
	if a.Restart == nil {
		return errors.New("restart is not available")
	}
	a.log.Warn("restart requested from the UI")
	a.Restart()
	return nil
}
