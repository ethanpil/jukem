package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"jukem/internal/events"
	"jukem/internal/player"
	"jukem/internal/scheduler"
	"jukem/internal/store"
)

// An announcement plays at its time, whatever the music does. When music
// plays, it fades out, the announcement plays, and the music fades in again
// at the point it stopped. The reconciler is held meanwhile, so it cannot
// put the program back while the announcement plays.

// announceTick is how often the announcements are checked. The grace in the
// scheduler is longer than this, so a check cannot step over a play time.
const announceTick = 20 * time.Second

// announceMax is the longest an announcement may hold the music.
const announceMax = 5 * time.Minute

// runAnnouncements plays every announcement that becomes due.
func (a *App) runAnnouncements(ctx context.Context) {
	t := time.NewTicker(announceTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.playDueAnnouncements(ctx)
		}
	}
}

// announcementsWanted reports whether an announcement may play now. It plays
// whether or not music plays, but it needs MPD and a clock it can trust.
func (a *App) announcementsWanted(ctx context.Context) error {
	if !a.MPD.Status().Running {
		return errors.New("MPD is not running")
	}
	if !a.Clock.Trusted(ctx) {
		// A clock that is wrong plays the announcement at the wrong time of
		// the day, which is worse than not playing it.
		return errors.New("the clock is not set")
	}
	return nil
}

// playDueAnnouncements plays the announcements that are due now, one after
// the other.
func (a *App) playDueAnnouncements(ctx context.Context) {
	if err := a.announcementsWanted(ctx); err != nil {
		return
	}
	list, err := a.Store.ListAnnouncements(ctx)
	if err != nil {
		a.log.Warn("cannot read the announcements", "error", err)
		return
	}
	set := a.Settings()
	loc := set.Location()
	now := a.Scheduler.Now()
	for _, ann := range list {
		due, ok := scheduler.AnnouncementDue(ann, now, loc)
		if !ok {
			continue
		}
		if err := a.PlayAnnouncement(ctx, ann, due); err != nil {
			a.log.Warn("announcement failed", "name", ann.Name, "error", err)
			a.Alerter.Raise(ctx, announcementAlert(ann), fmt.Sprintf("The announcement %q did not play.", ann.Name),
				"Check its file or folder in Schedule > Announcements.")
		}
	}
}

// announcementAlert is the alert kind of one announcement, so two broken
// announcements do not become one message.
func announcementAlert(ann store.Announcement) string {
	return fmt.Sprintf("announcement_%d", ann.ID)
}

// StartAnnouncement starts an announcement for a person who wants to hear it
// now. It answers when the announcement starts: the file lasts as long as it
// lasts, and the caller must not wait for it. A source that cannot play is
// reported at once.
func (a *App) StartAnnouncement(ctx context.Context, ann store.Announcement, due time.Time) error {
	if err := a.announcementsWanted(ctx); err != nil {
		return err
	}
	if _, _, err := a.announcementFile(ctx, ann); err != nil {
		return err
	}
	free := context.WithoutCancel(ctx)
	go func() {
		if err := a.PlayAnnouncement(free, ann, due); err != nil {
			a.log.Warn("the test play failed", "name", ann.Name, "error", err)
		}
	}()
	return nil
}

// PlayAnnouncement plays one announcement and gives the music back. A zero
// due time is a test from the UI: it moves a cycle on, but it does not take
// the place of the next play of the schedule.
func (a *App) PlayAnnouncement(ctx context.Context, ann store.Announcement, due time.Time) error {
	// One announcement at a time. Two of them would each try to give the
	// music back to the other one.
	if !a.announceMu.TryLock() {
		return errors.New("another announcement is playing")
	}
	defer a.announceMu.Unlock()

	file, next, err := a.announcementFile(ctx, ann)
	if err != nil {
		return err
	}
	// The play is written down before it happens. A record that fails after
	// the play would let the same time play again every twenty seconds.
	if err := a.markAnnouncement(ctx, ann, due, next); err != nil {
		return err
	}

	release := a.Scheduler.Suspend()
	defer release()
	a.announcing.Store(true)
	defer a.announcing.Store(false)

	before, err := a.Player.Status()
	if err != nil {
		return err
	}
	id, err := a.Player.InsertNext(file)
	if err != nil {
		return fmt.Errorf("cannot queue %s: %w", file, err)
	}
	// From here the music must come back, whatever happens.
	defer a.restoreAfterAnnouncement(ctx, before, id)

	a.makeRoomForAnnouncement(ctx, before, ann)
	if err := a.Player.PlayID(id); err != nil {
		return fmt.Errorf("cannot play %s: %w", file, err)
	}
	a.Events.Publish(events.Player, "")
	a.waitForAnnouncement(ctx, id)
	a.Alerter.Clear(ctx, announcementAlert(ann))
	a.Events.Publish(events.Schedule, "")
	return nil
}

// makeRoomForAnnouncement fades the music out and holds it where it is, then
// sets the level of the announcement. With no music it only sets the level.
func (a *App) makeRoomForAnnouncement(ctx context.Context, before player.Status, ann store.Announcement) {
	set := a.Settings()
	if before.State == "play" {
		if before.Volume > 0 {
			scheduler.Fade(ctx, a.Player.SetVolumeRaw, before.Volume, 0, set.FadeOut)
		}
		// The track keeps its position while the announcement plays.
		if err := a.Player.Pause(); err != nil {
			a.log.Warn("cannot hold the music for the announcement", "error", err)
		}
	}
	if before.Volume < 0 {
		// The output has no mixer, so there is no level to set.
		return
	}
	if ann.Volume != nil {
		// The volume of an announcement keeps the floor and the ceiling of
		// the settings, like every other volume a person sets.
		if _, err := a.Player.SetVolume(*ann.Volume); err == nil {
			return
		}
	}
	if err := a.Player.SetVolumeRaw(before.Volume); err != nil {
		a.log.Warn("cannot set the announcement volume", "error", err)
	}
}

// markAnnouncement writes the play time and the next position of a cycle.
// The write does not follow the caller's context: a browser that goes away
// must not leave the announcement due again.
func (a *App) markAnnouncement(ctx context.Context, ann store.Announcement, due time.Time, next int) error {
	free := context.WithoutCancel(ctx)
	if due.IsZero() {
		// A test play keeps the time of the schedule and moves the cycle.
		return a.Store.SetAnnouncementCycle(free, ann.ID, next)
	}
	return a.Store.MarkAnnouncementPlayed(free, ann.ID, due, next)
}

// waitForAnnouncement returns when the entry is no longer the current one,
// when playback stops, or at the longest hold.
func (a *App) waitForAnnouncement(ctx context.Context, id int) {
	deadline := time.Now().Add(announceMax)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		st, err := a.Player.Status()
		if err != nil {
			return
		}
		if st.Song == nil || st.Song.ID != id || st.State == "stop" {
			return
		}
		if time.Now().After(deadline) {
			a.log.Warn("the announcement plays too long, the music comes back", "id", id)
			return
		}
	}
}

// restoreAfterAnnouncement removes the announcement from the queue and puts
// the music back where it was. Music that played fades in again.
func (a *App) restoreAfterAnnouncement(ctx context.Context, before player.Status, id int) {
	// The music must come back even when the request that started this is
	// gone, so the fade does not follow the caller's context.
	ctx = context.WithoutCancel(ctx)
	set := a.Settings()
	if err := a.Player.Remove(id); err != nil {
		a.log.Debug("the announcement was already out of the queue", "error", err)
	}
	if before.Song == nil || before.State == "stop" {
		// Nothing played before, or it was stopped. It stays that way.
		if err := a.Player.Stop(); err != nil {
			a.log.Warn("cannot stop after the announcement", "error", err)
		}
		a.putVolumeBack(before)
		a.Events.Publish(events.Player, "")
		return
	}
	if before.State != "play" {
		// It was paused. To go back to its point it plays for a moment, so
		// it does so silently and keeps its level and its pause.
		if before.Volume >= 0 {
			a.setLevel(0)
		}
		a.toTrackAgain(before)
		if err := a.Player.Pause(); err != nil {
			a.log.Warn("cannot pause after the announcement", "error", err)
		}
		a.putVolumeBack(before)
		a.Events.Publish(events.Player, "")
		return
	}
	// The music played. The level is set before the track starts again, so
	// the first moment is never at the level of the announcement.
	fade := before.Volume > 0 && set.FadeIn > 0
	if before.Volume >= 0 {
		if fade {
			a.setLevel(0)
		} else {
			a.putVolumeBack(before)
		}
	}
	a.toTrackAgain(before)
	a.Events.Publish(events.Player, "")
	if fade {
		scheduler.Fade(ctx, a.Player.SetVolumeRaw, 0, before.Volume, set.FadeIn)
	}
}

// toTrackAgain starts the track of before again at its point.
func (a *App) toTrackAgain(before player.Status) {
	now, err := a.Player.Status()
	if err == nil && now.Song != nil && now.Song.ID == before.Song.ID {
		// The track is the current one again. To start it once more would
		// play it from the beginning, and a stream would load again.
		if now.State != "play" {
			if err := a.Player.Play(); err != nil {
				a.log.Warn("cannot start the music again", "error", err)
				return
			}
		}
	} else if err := a.Player.PlayID(before.Song.ID); err != nil {
		// The entry is gone, or MPD started again. The reconciler puts the
		// program back on its next tick.
		a.log.Warn("cannot start the music again", "error", err)
		return
	}
	// A stream has no position to go back to, and MPD refuses the seek.
	if before.Elapsed > 0 && !isStream(before.Song.File) {
		if err := a.Player.Seek(before.Elapsed); err != nil {
			a.log.Warn("cannot go back to the point in the track", "error", err)
		}
	}
}

// setLevel sets the output level, for the moments around an announcement.
func (a *App) setLevel(v int) {
	if err := a.Player.SetVolumeRaw(v); err != nil {
		a.log.Warn("cannot set the volume", "level", v, "error", err)
	}
}

// putVolumeBack sets the level the music had before the announcement.
func (a *App) putVolumeBack(before player.Status) {
	if before.Volume < 0 {
		return
	}
	if err := a.Player.SetVolumeRaw(before.Volume); err != nil {
		a.log.Warn("cannot put the volume back", "error", err)
	}
}

// isStream reports whether a queue entry is an address and not a file.
func isStream(file string) bool {
	return strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://")
}

// announcementFile chooses the file to play and the next position of a
// cycle.
func (a *App) announcementFile(ctx context.Context, ann store.Announcement) (string, int, error) {
	ref := strings.Trim(ann.SourceRef, "/")
	if ref == "" {
		return "", 0, errors.New("the announcement has no file or folder")
	}
	switch ann.SourceKind {
	case "file":
		return ref, ann.CycleIndex, nil
	case "random", "cycle":
		files, err := a.Player.ListFiles(ref)
		if err != nil {
			return "", 0, err
		}
		files = a.withoutDoNotPlay(ctx, files)
		if len(files) == 0 {
			return "", 0, fmt.Errorf("the folder %q holds no tracks", ann.SourceRef)
		}
		// The order of a cycle comes from the names, so it does not follow
		// the order MPD gives.
		sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i]) < strings.ToLower(files[j]) })
		if ann.SourceKind == "random" {
			return files[rand.IntN(len(files))], ann.CycleIndex, nil
		}
		i := ann.CycleIndex % len(files)
		if i < 0 {
			i = 0
		}
		return files[i], (i + 1) % len(files), nil
	}
	return "", 0, fmt.Errorf("unknown announcement source %q", ann.SourceKind)
}

// withoutDoNotPlay drops the tracks that must never play. The list comes
// from MPD and belongs to this call, so it is filtered in place.
func (a *App) withoutDoNotPlay(ctx context.Context, files []string) []string {
	dnp, err := a.Store.DoNotPlaySet(ctx)
	if err != nil || len(dnp) == 0 {
		return files
	}
	kept := files[:0]
	for _, f := range files {
		if !dnp[f] {
			kept = append(kept, f)
		}
	}
	return kept
}
