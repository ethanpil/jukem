package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"path"
	"sort"
	"strings"
	"time"

	"jukem/internal/events"
	"jukem/internal/player"
	"jukem/internal/scheduler"
	"jukem/internal/store"
)

// An announcement interrupts the music, plays one file, and gives the music
// back at the point it stopped. The reconciler is held meanwhile, so it
// cannot put the program back while the announcement plays.

// announceTick is how often the announcements are checked. The grace in the
// scheduler is longer than this, so a check cannot step over a play time.
const announceTick = 20 * time.Second

// announceMax is the longest an announcement may hold the music.
const announceMax = 10 * time.Minute

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

// playDueAnnouncements plays the announcements that are due now, one after
// the other.
func (a *App) playDueAnnouncements(ctx context.Context) {
	if !a.MPD.Status().Running {
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
			a.Alerter.Raise(ctx, "announcement", fmt.Sprintf("The announcement %q did not play.", ann.Name),
				"Check the file in Schedule > Announcements.")
		}
	}
}

// PlayAnnouncement plays one announcement now and gives the music back.
// The play time is recorded, so the same time does not play twice.
func (a *App) PlayAnnouncement(ctx context.Context, ann store.Announcement, due time.Time) error {
	file, next, err := a.announcementFile(ctx, ann)
	if err != nil {
		return err
	}
	release := a.Scheduler.Suspend()
	defer release()

	before, err := a.Player.Status()
	if err != nil {
		return err
	}
	id, err := a.Player.InsertNext(file)
	if err != nil {
		return fmt.Errorf("cannot queue %s: %w", file, err)
	}
	// From here the music must come back, whatever happens.
	defer a.restoreAfterAnnouncement(before, id)

	if ann.Volume != nil {
		if err := a.Player.SetVolumeRaw(*ann.Volume); err != nil {
			a.log.Warn("cannot set the announcement volume", "error", err)
		}
	}
	if err := a.Player.PlayID(id); err != nil {
		return fmt.Errorf("cannot play %s: %w", file, err)
	}
	a.Events.Publish(events.Player, "")
	a.waitForAnnouncement(ctx, id)

	// A test play from the UI passes no due time: it moves the cycle on, but
	// it does not take the place of the next play of the schedule.
	played := due
	if played.IsZero() {
		if ann.LastPlayed != nil {
			played = *ann.LastPlayed
		} else {
			played = time.Unix(0, 0).UTC()
		}
	}
	if err := a.Store.MarkAnnouncementPlayed(ctx, ann.ID, played, next); err != nil {
		a.log.Warn("cannot record the announcement", "error", err)
	}
	a.Events.Publish(events.Schedule, "")
	return nil
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
// the music back where it was.
func (a *App) restoreAfterAnnouncement(before player.Status, id int) {
	if err := a.Player.Remove(id); err != nil {
		a.log.Debug("the announcement was already out of the queue", "error", err)
	}
	if before.Volume >= 0 {
		if err := a.Player.SetVolumeRaw(before.Volume); err != nil {
			a.log.Warn("cannot put the volume back", "error", err)
		}
	}
	if before.Song == nil {
		if err := a.Player.Stop(); err != nil {
			a.log.Warn("cannot stop after the announcement", "error", err)
		}
		a.Events.Publish(events.Player, "")
		return
	}
	if err := a.Player.PlayID(before.Song.ID); err != nil {
		a.log.Warn("cannot start the music again", "error", err)
		a.Events.Publish(events.Player, "")
		return
	}
	if before.Elapsed > 0 {
		if err := a.Player.Seek(before.Elapsed); err != nil {
			a.log.Warn("cannot go back to the point in the track", "error", err)
		}
	}
	if before.State != "play" {
		if err := a.Player.Pause(); err != nil {
			a.log.Warn("cannot pause after the announcement", "error", err)
		}
	}
	a.Events.Publish(events.Player, "")
}

// announcementFile chooses the file to play and the next position of a
// cycle.
func (a *App) announcementFile(ctx context.Context, ann store.Announcement) (string, int, error) {
	ref := strings.Trim(ann.SourceRef, "/")
	switch ann.SourceKind {
	case "file":
		if ref == "" {
			return "", 0, errors.New("the announcement has no file")
		}
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
		// The order of a cycle must not change when a file is added, so
		// the files are sorted by name.
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

// withoutDoNotPlay drops the tracks that must never play.
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

// AnnouncementSourceName is the file or folder an announcement plays, for a
// message to a person.
func AnnouncementSourceName(ann store.Announcement) string {
	if ann.SourceKind == "file" {
		return path.Base(ann.SourceRef)
	}
	return ann.SourceRef
}
