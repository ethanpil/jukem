package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"jukem/internal/events"
	"jukem/internal/player"
	"jukem/internal/scheduler"
	"jukem/internal/store"
)

// ownerNow reports the owner for the API and the health report.
func (a *App) ownerNow() player.Owner {
	return a.Scheduler.Owner(context.Background())
}

// resolveProgram resolves a schedule source to files, without the
// do-not-play tracks.
func (a *App) resolveProgram(ctx context.Context, src scheduler.Source) ([]string, bool, error) {
	var files []string
	var err error
	switch src.Type {
	case "directory":
		files, err = a.Player.ListFiles(strings.Trim(src.Ref, "/"))
	case "playlist":
		id, perr := strconv.ParseInt(src.Ref, 10, 64)
		if perr != nil {
			return nil, false, fmt.Errorf("playlist reference %q is not an id", src.Ref)
		}
		pl, ok, gerr := a.Store.GetPlaylist(ctx, id)
		if gerr != nil {
			return nil, false, gerr
		}
		if !ok {
			return nil, false, fmt.Errorf("playlist %d no longer exists", id)
		}
		files, err = a.Playlists.PresentEntries(pl.Name)
	default:
		return nil, false, fmt.Errorf("unknown source type %q", src.Type)
	}
	if err != nil {
		return nil, false, err
	}
	dnp, err := a.Store.DoNotPlaySet(ctx)
	if err != nil {
		return nil, false, err
	}
	if len(dnp) > 0 {
		kept := files[:0]
		for _, f := range files {
			if !dnp[f] {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	truncated := len(files) > player.MaxQueue
	if truncated {
		files = files[:player.MaxQueue]
	}
	return files, truncated, nil
}

// holdForPerson records a transport action while the scheduler is on, so
// the schedule does not undo it five seconds later. An active timed
// override keeps its end and only changes its intent. Manual mode needs
// no override.
func (a *App) holdForPerson(ctx context.Context, intent, source string) error {
	if !a.Settings().SchedulerEnabled {
		return nil
	}
	if o, _, active := a.Scheduler.Override(ctx); active && o.Mode == "timed" {
		if o.Intent == intent {
			return nil
		}
		return a.Scheduler.UpdateOverrideIntent(ctx, o, intent)
	}
	return a.Scheduler.CreateOverride(ctx, store.Override{Mode: "until_next", Intent: intent, Source: source})
}

// Transport runs play, pause or stop for a caller and records the
// override. The loop is held meanwhile, so a tick cannot undo the action
// before the override exists.
func (a *App) Transport(ctx context.Context, action, source string) error {
	release := a.Scheduler.Suspend()
	defer release()
	var err error
	switch action {
	case "play":
		err = a.Player.Play()
	case "pause":
		err = a.Player.Pause()
	case "stop":
		err = a.Player.Stop()
	default:
		return fmt.Errorf("unknown action %s", action)
	}
	if err != nil {
		return err
	}
	return a.holdForPerson(ctx, action, source)
}

// PlayEntry plays from a queue entry, which counts as a play for the
// schedule.
func (a *App) PlayEntry(ctx context.Context, id int, source string) error {
	release := a.Scheduler.Suspend()
	defer release()
	if err := a.Player.PlayID(id); err != nil {
		return err
	}
	return a.holdForPerson(ctx, "play", source)
}

// playNowOverride records a Play Now: the override ends when the chosen
// tracks finish or at the next scheduled event. The caller holds the
// loop.
func (a *App) playNowOverride(ctx context.Context, source string) error {
	a.Scheduler.ForgetProgram(ctx)
	if !a.Settings().SchedulerEnabled {
		return nil
	}
	return a.Scheduler.CreateOverride(ctx, store.Override{Mode: "play_now", Intent: "play", Source: source})
}

// applySchedulerSwitch runs after the scheduler setting changed.
func (a *App) applySchedulerSwitch(ctx context.Context, enabled bool) {
	if !enabled {
		if err := a.Store.ClearOverride(ctx); err != nil {
			a.log.Warn("cannot clear the override", "error", err)
		}
	}
	a.Scheduler.Invalidate()
	a.Events.Publish(events.Schedule, "")
}
