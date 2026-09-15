package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/api"
	"jukem/internal/audio"
	"jukem/internal/events"
	"jukem/internal/mpdctl"
	"jukem/internal/player"
	"jukem/internal/store"
)

// watchMPD follows MPD's idle events, turns them into client
// notifications, and keeps the player's track bookkeeping current.
func (a *App) watchMPD(ctx context.Context) {
	socket := a.MPD.Paths().Socket
	lastSong := -1
	for sub := range mpdctl.Watch(ctx, socket, a.log, "player", "mixer", "options", "playlist", "update", "database", "output") {
		switch sub {
		case "player", "mixer", "options":
			a.Events.Publish(events.Player, "")
			if sub == "player" {
				if st, err := a.Player.Status(); err == nil && st.Song != nil {
					a.Player.NoteSong(st.Song.File, st.Song.Pos)
					if st.Song.ID != lastSong {
						lastSong = st.Song.ID
						a.Player.SongChanged(st.Song.ID)
						a.onSongChange(st)
					}
				}
			}
		case "playlist":
			a.Events.Publish(events.Queue, "")
		case "update", "database":
			a.Events.Publish(events.Library, "")
		case "output":
			a.Events.Publish(events.Devices, "")
		}
	}
}

// onSongChange runs when a new track starts. Later steps record history
// here.
func (a *App) onSongChange(st player.Status) {}

// Owner reports who decides playback. Until the scheduler exists, the
// switch alone decides: on means scheduled, off means manual.
func (a *App) Owner() player.Owner {
	set := a.Settings()
	if !set.SchedulerEnabled {
		return player.Owner{State: player.OwnerManual, Reason: "Scheduler off"}
	}
	if !a.MPD.Status().Running {
		return player.Owner{State: player.OwnerUnavailable, Reason: "MPD is not running"}
	}
	if a.Devices.Snapshot().Selected == nil {
		return player.Owner{State: player.OwnerUnavailable, Reason: "Output device missing"}
	}
	return player.Owner{State: player.OwnerScheduled, Reason: "No schedule loaded"}
}

// Transport runs play, pause or stop for a caller.
func (a *App) Transport(ctx context.Context, action, source string) error {
	var err error
	switch action {
	case "play":
		err = a.Player.Play()
	case "pause":
		err = a.Player.Pause()
	case "stop":
		err = a.Player.Stop()
	default:
		return errors.New("unknown action " + action)
	}
	return err
}

// PlayEntry plays from a queue entry.
func (a *App) PlayEntry(ctx context.Context, id int, source string) error {
	return a.Player.PlayID(id)
}

// resolveSource turns a queue action into an ordered list of files.
func (a *App) resolveSource(ctx context.Context, q api.QueueAction) ([]string, bool, error) {
	switch {
	case len(q.Files) > 0:
		return q.Files, false, nil
	case q.Folder != "" || q.IsRoot:
		files, err := a.Player.ListFiles(strings.Trim(q.Folder, "/"))
		if err != nil {
			return nil, false, err
		}
		if len(files) == 0 {
			return nil, false, huma.Error404NotFound("no tracks in " + q.Folder)
		}
		truncated := len(files) > player.MaxQueue
		if truncated {
			files = files[:player.MaxQueue]
		}
		return files, truncated, nil
	case q.Playlist != 0:
		files, err := a.playlistFiles(ctx, q.Playlist)
		if err != nil {
			return nil, false, err
		}
		return files, false, nil
	}
	return nil, false, huma.Error422UnprocessableEntity("give files, a folder or a playlist")
}

// playlistFiles is completed in build step 8.
func (a *App) playlistFiles(ctx context.Context, id int64) ([]string, error) {
	return nil, huma.Error404NotFound(fmt.Sprintf("no playlist %d", id))
}

// QueueAction applies play_now, play_next or add.
func (a *App) QueueAction(ctx context.Context, q api.QueueAction, source string) (api.QueueResult, error) {
	files, truncated, err := a.resolveSource(ctx, q)
	if err != nil {
		return api.QueueResult{}, err
	}
	st, _ := a.Player.Status()
	res := api.QueueResult{Truncated: truncated, Shuffle: st.Shuffle}
	switch q.Action {
	case "play_now":
		res.Added, err = a.Player.Load(files, st.Shuffle, -1)
	case "play_next":
		res.Added, err = a.Player.PlayNext(files)
	case "add":
		res.Added, err = a.Player.Add(files)
	default:
		return res, huma.Error422UnprocessableEntity("unknown action " + q.Action)
	}
	if err == nil && res.Added < len(files) {
		res.Truncated = true
	}
	return res, err
}

// SelectOutput saves the selection and applies it.
func (a *App) SelectOutput(ctx context.Context, id *audio.Identity) error {
	set := a.Settings()
	set.OutputDevice = id
	if err := a.saveSettings(ctx, set); err != nil {
		return err
	}
	if err := a.Devices.SetSaved(ctx, id); err != nil {
		a.log.Warn("device rescan after selection failed", "error", err)
	}
	// The device set is unchanged, so no restart follows; apply the
	// selection now.
	go a.applyOutput()
	a.Events.Publish(events.Devices, "")
	return nil
}

func (a *App) saveSettings(ctx context.Context, set store.Settings) error {
	if err := a.Store.SaveSettings(ctx, set); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = set
	a.mu.Unlock()
	return nil
}

// UpdateSettings stores new settings and applies what changed.
func (a *App) UpdateSettings(ctx context.Context, set store.Settings) error {
	old := a.Settings()
	// The output selection has its own endpoint; a settings PUT keeps it.
	set.OutputDevice = old.OutputDevice
	if _, err := time.LoadLocation(set.TimeZone); err != nil {
		return huma.Error422UnprocessableEntity("unknown time zone " + set.TimeZone)
	}
	if err := a.saveSettings(ctx, set); err != nil {
		return err
	}
	if set.MusicRoot != old.MusicRoot {
		a.mu.Lock()
		outputs := a.outputs
		a.mu.Unlock()
		a.log.Info("music root changed, restarting mpd", "root", set.MusicRoot)
		if err := a.MPD.Reconfigure(mpdctl.NewConfig(a.cfg.DataDir, set.MusicRoot, outputs)); err != nil {
			return err
		}
		a.rescanAfterStart = true
	}
	if set.Crossfade != old.Crossfade {
		if err := a.Player.SetCrossfade(set.Crossfade); err != nil {
			a.log.Warn("cannot set crossfade", "error", err)
		}
	}
	a.Events.Publish(events.Settings, "")
	return nil
}

// volumeLimits returns the configured floor and ceiling.
func (a *App) volumeLimits() (int, int) {
	set := a.Settings()
	return set.VolumeMin, set.VolumeMax
}

// applyPlayerSettings runs after every MPD start: crossfade and the
// scheduled-playback repeat mode.
func (a *App) applyPlayerSettings() {
	set := a.Settings()
	if err := a.Player.SetCrossfade(set.Crossfade); err != nil {
		a.log.Warn("cannot set crossfade", "error", err)
	}
	a.mu.Lock()
	rescan := a.rescanAfterStart
	a.rescanAfterStart = false
	a.mu.Unlock()
	if rescan {
		if _, err := a.Player.Update(""); err != nil {
			a.log.Warn("cannot start the library rescan", "error", err)
		}
	}
}
