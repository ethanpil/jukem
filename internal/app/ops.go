package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/api"
	"jukem/internal/audio"
	"jukem/internal/events"
	"jukem/internal/library"
	"jukem/internal/mpdctl"
	"jukem/internal/player"
	"jukem/internal/store"
)

// watchMPD follows MPD's idle events, turns them into client
// notifications, and keeps the player's track bookkeeping current.
func (a *App) watchMPD(ctx context.Context) {
	socket := a.MPD.Paths().Socket
	// Queue ids restart with each MPD process, so the last song is keyed
	// by id and queue version together.
	lastSong := ""
	for sub := range mpdctl.Watch(ctx, socket, a.log, "player", "mixer", "options", "playlist", "update", "database", "output") {
		switch sub {
		case "player", "mixer", "options":
			a.Events.Publish(events.Player, "")
			if sub == "player" {
				st, err := a.Player.Status()
				if err != nil || st.Song == nil {
					continue
				}
				a.Player.NoteSong(st)
				key := fmt.Sprintf("%d/%d/%s", a.MPD.Status().PID, st.Song.ID, st.Song.File)
				if key != lastSong {
					lastSong = key
					a.Player.SongChanged(st.Song.ID)
					a.onSongChange(st)
				}
			}
		case "playlist":
			a.Events.Publish(events.Queue, "")
		case "update", "database":
			a.Events.Publish(events.Library, "")
			if a.Files.ScanPending() {
				if st, err := a.Player.Status(); err == nil {
					a.Files.NoteUpdate(st.Updating)
				}
			}
		case "output":
			a.Events.Publish(events.Devices, "")
		}
	}
}

// onSongChange runs when a new track starts. Later steps record history
// here.
func (a *App) onSongChange(st player.Status) {}

// Owner reports who decides playback. MPD down is always UNAVAILABLE. A
// missing output makes the owner UNAVAILABLE only while the scheduler is
// on; in manual mode it is a warning, because a person pressing play has
// no schedule to get wrong.
func (a *App) Owner() player.Owner {
	if !a.MPD.Status().Running {
		return player.Owner{State: player.OwnerUnavailable, Reason: "MPD is not running"}
	}
	set := a.Settings()
	missing := a.Devices.Snapshot().Selected == nil
	if !set.SchedulerEnabled {
		o := player.Owner{State: player.OwnerManual, Reason: "Scheduler off"}
		if missing {
			o.Warning = "Output device missing"
		}
		return o
	}
	if missing {
		return player.Owner{State: player.OwnerUnavailable, Reason: "Output device missing"}
	}
	return player.Owner{State: player.OwnerScheduled, Reason: "No schedule loaded"}
}

// Transport runs play, pause or stop for a caller.
func (a *App) Transport(ctx context.Context, action, source string) error {
	switch action {
	case "play":
		return a.Player.Play()
	case "pause":
		return a.Player.Pause()
	case "stop":
		return a.Player.Stop()
	}
	return huma.Error422UnprocessableEntity("unknown action " + action)
}

// PlayEntry plays from a queue entry.
func (a *App) PlayEntry(ctx context.Context, id int, source string) error {
	return a.Player.PlayID(id)
}

// resolveSource turns a queue action into an ordered list of files.
func (a *App) resolveSource(ctx context.Context, q api.QueueAction) ([]string, error) {
	switch {
	case len(q.Files) > 0:
		return q.Files, nil
	case q.Folder != "":
		files, err := a.Player.ListFiles(strings.Trim(q.Folder, "/"))
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, huma.Error404NotFound("no tracks in " + q.Folder)
		}
		return files, nil
	case q.Playlist != 0:
		return a.playlistFiles(ctx, q.Playlist)
	}
	return nil, huma.Error422UnprocessableEntity("give files, a folder or a playlist")
}

// playlistFiles is completed in build step 8.
func (a *App) playlistFiles(ctx context.Context, id int64) ([]string, error) {
	return nil, huma.Error404NotFound(fmt.Sprintf("no playlist %d", id))
}

// QueueAction applies play_now, play_next or add. Play Now keeps the
// current shuffle setting.
func (a *App) QueueAction(ctx context.Context, q api.QueueAction, source string) (api.QueueResult, error) {
	files, err := a.resolveSource(ctx, q)
	if err != nil {
		return api.QueueResult{}, err
	}
	var res api.QueueResult
	switch q.Action {
	case "play_now":
		st, err := a.Player.Status()
		if err != nil {
			return res, err
		}
		res.Shuffle = st.Shuffle
		res.Added, err = a.Player.Load(files, st.Shuffle, -1)
		if err != nil {
			return res, err
		}
	case "play_next":
		res.Added, err = a.Player.PlayNext(files)
	case "add":
		res.Added, err = a.Player.Add(files)
	default:
		return res, huma.Error422UnprocessableEntity("unknown action " + q.Action)
	}
	if err != nil {
		return res, err
	}
	res.Truncated = res.Added < len(files)
	if q.Action != "play_now" {
		if st, err := a.Player.Status(); err == nil {
			res.Shuffle = st.Shuffle
		}
	}
	return res, nil
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
	// The device set did not change, so MPD does not restart. Apply the
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
	// The output selection has its own endpoint. A settings PUT keeps it.
	set.OutputDevice = old.OutputDevice
	if _, err := time.LoadLocation(set.TimeZone); err != nil {
		return huma.Error422UnprocessableEntity("unknown time zone " + set.TimeZone)
	}
	if err := a.saveSettings(ctx, set); err != nil {
		return err
	}
	if set.MusicRoot != old.MusicRoot {
		library.ForgetStat()
		a.mu.Lock()
		outputs := a.outputs
		a.rescanAfterStart = true
		a.mu.Unlock()
		a.log.Info("music root changed, restarting mpd", "root", set.MusicRoot)
		if err := a.MPD.Reconfigure(mpdctl.NewConfig(a.cfg.DataDir, set.MusicRoot, outputs)); err != nil {
			return err
		}
	}
	if set.Crossfade != old.Crossfade {
		if err := a.Player.SetCrossfade(set.Crossfade); err != nil {
			a.log.Warn("cannot set crossfade", "error", err)
		}
	}
	a.Events.Publish(events.Settings, "")
	if set.SchedulerEnabled != old.SchedulerEnabled {
		a.Events.Publish(events.Schedule, "")
	}
	return nil
}

// uploadLimits returns the upload rules from the settings.
func (a *App) uploadLimits() library.Limits {
	set := a.Settings()
	return library.Limits{MaxBytes: set.UploadMaxBytes, Reserve: set.FreeSpaceReserve, Extension: set.ExtensionAllowed}
}

// volumeLimits returns the configured floor and ceiling.
func (a *App) volumeLimits() (int, int) {
	set := a.Settings()
	return set.VolumeMin, set.VolumeMax
}

// applyPlayerSettings runs after every MPD start: the crossfade, and a
// full rescan when the music root changed.
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
