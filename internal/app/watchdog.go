package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"jukem/internal/library"
	"jukem/internal/player"
	"jukem/internal/store"
)

// watchInterval is the time between two watchdog checks.
const watchInterval = 30 * time.Second

// stallState remembers what the last check saw, so that a track whose
// elapsed time does not move is noticed.
type stallState struct {
	songID      int
	elapsed     float64
	stuck       int       // checks in a row without progress
	restartedAt time.Time // the last MPD restart for a stall
	resume      bool      // press play after the restart: MPD comes back paused
}

// runWatchdog checks playback, the disk and the output device until ctx
// ends. Every problem becomes an alert, and clears when it is gone.
func (a *App) runWatchdog(ctx context.Context) {
	t := time.NewTicker(watchInterval)
	defer t.Stop()
	var stall stallState
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.checkStall(ctx, &stall)
		a.checkDisk(ctx)
		a.checkDevice(ctx)
		if mpd := a.MPD.Status(); mpd.Running && time.Since(mpd.Since) > 5*time.Minute {
			a.Alerter.Clear(ctx, "mpd_unstable")
		}
	}
}

// checkStall finds a playing track whose elapsed time does not change.
// A hung USB DAC causes this. The first time, jukem restarts MPD. When
// the stall continues in the ten minutes after that, jukem raises an
// alert.
func (a *App) checkStall(ctx context.Context, s *stallState) {
	st, err := a.Player.Status()
	if err != nil {
		s.stuck = 0
		return
	}
	if s.resume && st.State == "pause" {
		// The restart was for a stall while playing, so play again. In
		// manual mode nothing else does.
		s.resume = false
		a.Player.Play()
		return
	}
	if st.State != "play" || st.Song == nil {
		s.stuck = 0
		a.Alerter.Clear(ctx, "playback_stalled")
		return
	}
	if st.Song.ID == s.songID && st.Elapsed == s.elapsed {
		s.stuck++
	} else {
		s.stuck = 0
		a.Alerter.Clear(ctx, "playback_stalled")
	}
	s.songID, s.elapsed = st.Song.ID, st.Elapsed
	if s.stuck < 2 {
		return
	}
	name := st.Song.Title
	if name == "" {
		name = st.Song.File
	}
	if time.Since(s.restartedAt) > 10*time.Minute {
		a.log.Warn("playback stalled, restarting mpd", "track", name)
		s.restartedAt = time.Now()
		s.stuck = 0
		s.resume = true
		a.MPD.Restart()
		return
	}
	a.Alerter.Raise(ctx, "playback_stalled",
		fmt.Sprintf("Playback is stuck: %s has not advanced for a minute, and a restart of MPD did not help.", name),
		"Unplug the output device and plug it in again, or choose another output in Settings > Audio.")
}

// checkDisk raises an alert when the music disk has less than the free
// space reserve, because uploads are refused from then on.
func (a *App) checkDisk(ctx context.Context) {
	set := a.Settings()
	st := library.Stat(set.MusicRoot)
	if st.Missing || st.TotalBytes == 0 {
		return
	}
	if st.FreeBytes < set.FreeSpaceReserve {
		a.Alerter.Raise(ctx, "disk_full",
			fmt.Sprintf("The music disk is nearly full: %s free, below the reserve of %s. Uploads are refused.", humanBytes(st.FreeBytes), humanBytes(set.FreeSpaceReserve)),
			"Delete music you do not need, or move the library to a larger disk.")
		return
	}
	a.Alerter.Clear(ctx, "disk_full")
}

// checkDevice raises an alert while the selected output is unplugged.
func (a *App) checkDevice(ctx context.Context) {
	snap := a.Devices.Snapshot()
	if snap.Saved != nil && snap.Selected == nil {
		a.Alerter.Raise(ctx, "device_missing",
			"The output device "+snap.Saved.Name+" is missing. Nothing plays until it is back.",
			"Plug the device in again, or choose another output in Settings > Audio.")
		return
	}
	a.Alerter.Clear(ctx, "device_missing")
}

// onSchedulerProblem turns dead-air reports into alerts.
func (a *App) onSchedulerProblem(kind, message string) {
	ctx := context.Background()
	switch kind {
	case "dead_air":
		a.Alerter.Raise(ctx, "dead_air", "The schedule says play, but nothing plays: "+message,
			"Open the Health page. Check the output device, the music root and the program's source.")
	case "dead_air_cleared":
		a.Alerter.Clear(ctx, "dead_air")
	}
}

// onSongChange records a track start in the history.
func (a *App) onSongChange(st player.Status) {
	ctx := context.Background()
	row := store.HistoryRow{StartedAt: time.Now(), File: st.Song.File, Title: st.Song.Title, Artist: st.Song.Artist, Album: st.Song.Album, Source: a.Scheduler.PlaySource(ctx)}
	if err := a.Store.AddHistory(ctx, row); err != nil {
		a.log.Warn("cannot record the play history", "error", err)
	}
}

// runNightly runs the full rescan, the retention trim and the playlist
// check once a day at the configured hour, in the configured zone.
func (a *App) runNightly(ctx context.Context) {
	for {
		set := a.Settings()
		loc, err := time.LoadLocation(set.TimeZone)
		if err != nil {
			loc = time.UTC
		}
		now := a.Clock.Now().In(loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), set.NightlyRescanHour, 0, 0, 0, loc)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		// Wake at most an hour later, so a changed hour or zone applies
		// without a restart.
		wait := min(next.Sub(now), time.Hour)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < time.Hour || !a.Clock.Now().In(loc).Before(next) {
			a.nightly(ctx)
		}
	}
}

// nightly runs the full rescan, the retention trim and the playlist
// check.
func (a *App) nightly(ctx context.Context) {
	set := a.Settings()
	a.log.Info("nightly job", "rescan", true)
	if _, err := a.Player.Update(""); err != nil {
		a.log.Warn("nightly rescan failed", "error", err)
	}
	if err := a.Store.TrimHistory(ctx, set.HistoryDays, set.HistoryRows); err != nil {
		a.log.Warn("cannot trim the history", "error", err)
	}
	if err := a.Store.TrimAlerts(ctx, set.AlertDays); err != nil {
		a.log.Warn("cannot trim the alerts", "error", err)
	}
	a.checkPlaylists(ctx)
}

// checkPlaylists raises an alert for playlist entries whose files are
// missing. Files removed outside jukem cause this.
func (a *App) checkPlaylists(ctx context.Context) {
	lists, err := a.Store.ListPlaylists(ctx)
	if err != nil {
		return
	}
	missing := 0
	var names []string
	for _, pl := range lists {
		entries, err := a.Playlists.EntriesWithState(pl.Name)
		if err != nil {
			continue
		}
		n := 0
		for _, e := range entries {
			if e.Missing {
				n++
			}
		}
		if n > 0 {
			missing += n
			names = append(names, pl.Name)
		}
	}
	if missing == 0 {
		a.Alerter.Clear(ctx, "playlist_missing_files")
		return
	}
	a.Alerter.Raise(ctx, "playlist_missing_files",
		fmt.Sprintf("%d playlist entries point at files that no longer exist, in: %s.", missing, strings.Join(names, ", ")),
		"Open Playlists and remove the entries marked missing, or put the files back.")
}

// humanBytes writes a byte count with a unit.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	if v >= 10 {
		return fmt.Sprintf("%.0f %cB", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", v, "KMGTPE"[exp])
}
