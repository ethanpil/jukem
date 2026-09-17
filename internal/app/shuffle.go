package app

import (
	"context"
	"errors"
)

// SetShuffle turns the shuffle on or off. A shuffle changes the order of
// the queue, so the reconciler is held meanwhile. It cannot run while an
// announcement plays: the announcements of a group are queue entries, and
// a reorder would break their order.
func (a *App) SetShuffle(ctx context.Context, on bool) error {
	if a.announcing.Load() {
		return errors.New("an announcement is playing, try again in a moment")
	}
	release := a.Scheduler.Suspend()
	defer release()
	if a.announcing.Load() {
		return errors.New("an announcement is playing, try again in a moment")
	}
	return a.Player.SetShuffle(on)
}
