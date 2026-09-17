package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
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

// dueAnnouncement is a request to play an announcement. due is the time it
// plays for, and a zero time is a test play from the UI. fail hears why it
// did not play.
type dueAnnouncement struct {
	ann  store.Announcement
	due  time.Time
	fail func(error)
}

// playDueAnnouncements asks for the announcements that are due now. The
// group that plays takes them in, or a new group starts.
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
	var due []dueAnnouncement
	for _, ann := range list {
		t, ok := scheduler.AnnouncementDue(ann, now, loc)
		if !ok {
			continue
		}
		due = append(due, dueAnnouncement{ann: ann, due: t, fail: func(err error) {
			a.log.Warn("announcement failed", "name", ann.Name, "error", err)
			// The next check can try this time again, while the grace lasts.
			a.forgetAnnouncement(ann.ID, t)
			a.Alerter.Raise(ctx, announcementAlert(ann), fmt.Sprintf("The announcement %q did not play.", ann.Name),
				"Check its file or folder in Schedule > Announcements.")
		}})
	}
	a.requestAnnouncements(ctx, due)
}

// announcementAlert is the alert kind of one announcement, so two broken
// announcements do not become one message.
func announcementAlert(ann store.Announcement) string {
	return fmt.Sprintf("announcement_%d", ann.ID)
}

// StartAnnouncement starts an announcement for a person who wants to hear it
// now. It answers when the announcement starts: the file lasts as long as it
// lasts, and the caller must not wait for it. A source that cannot play is
// reported at once. When other announcements play, it plays after them.
func (a *App) StartAnnouncement(ctx context.Context, ann store.Announcement, due time.Time) error {
	if err := a.announcementsWanted(ctx); err != nil {
		return err
	}
	if _, _, err := a.announcementFile(ctx, ann); err != nil {
		return err
	}
	a.requestAnnouncements(context.WithoutCancel(ctx), []dueAnnouncement{{ann: ann, due: due, fail: func(err error) {
		a.log.Warn("the test play failed", "name", ann.Name, "error", err)
	}}})
	return nil
}

// addAnnouncements puts requests on the waiting list. A scheduled time that
// is already on the list, or played, is not added again. It reports whether
// a new group must start, because no group plays.
func (a *App) addAnnouncements(list []dueAnnouncement) bool {
	a.announceMu.Lock()
	defer a.announceMu.Unlock()
	if a.announceTaken == nil {
		a.announceTaken = map[int64]time.Time{}
	}
	for _, d := range list {
		if !d.due.IsZero() {
			if t, ok := a.announceTaken[d.ann.ID]; ok && !t.Before(d.due) {
				continue
			}
			a.announceTaken[d.ann.ID] = d.due
		}
		a.announceWaiting = append(a.announceWaiting, d)
	}
	if a.announceRunning || len(a.announceWaiting) == 0 {
		return false
	}
	a.announceRunning = true
	return true
}

// forgetAnnouncement lets a scheduled time that did not play be asked for
// again.
func (a *App) forgetAnnouncement(id int64, due time.Time) {
	a.announceMu.Lock()
	defer a.announceMu.Unlock()
	if t, ok := a.announceTaken[id]; ok && t.Equal(due) {
		delete(a.announceTaken, id)
	}
}

// takeAnnouncements empties the waiting list. When the list is empty and
// last is true, the group ends, so the next request starts a new one.
func (a *App) takeAnnouncements(last bool) []dueAnnouncement {
	a.announceMu.Lock()
	defer a.announceMu.Unlock()
	list := a.announceWaiting
	a.announceWaiting = nil
	if last && len(list) == 0 {
		a.announceRunning = false
	}
	return list
}

// requestAnnouncements asks for announcements. Only one group plays at a
// time: two of them would each try to give the music back to the other.
func (a *App) requestAnnouncements(ctx context.Context, list []dueAnnouncement) {
	if a.addAnnouncements(list) {
		go a.playAnnouncementGroups(ctx)
	}
}

// playAnnouncementGroups plays groups until no request waits.
func (a *App) playAnnouncementGroups(ctx context.Context) {
	for {
		list := a.takeAnnouncements(true)
		if len(list) == 0 {
			return
		}
		a.playAnnouncements(ctx, list)
		// A track start during the group did not reach the watcher, so
		// the queue gets its new order here.
		if st, err := a.Player.Status(); err == nil {
			if err := a.Player.ReshuffleAtEnd(st); err != nil {
				a.log.Warn("cannot shuffle the queue again", "error", err)
			}
		}
	}
}

// queuedAnnouncement is an announcement in the queue, by its queue id.
type queuedAnnouncement struct {
	ann store.Announcement
	id  int
}

// readyAnnouncement is an announcement with its chosen file.
type readyAnnouncement struct {
	ann  store.Announcement
	file string
	fail func(error)
}

// prepareAnnouncements chooses the files and writes the plays down.
func (a *App) prepareAnnouncements(ctx context.Context, list []dueAnnouncement) []readyAnnouncement {
	var ready []readyAnnouncement
	for _, d := range list {
		// A request can wait while other announcements play. Another play
		// of the same announcement can move its cycle meanwhile, and a
		// person can change or remove it.
		ann, ok, err := a.Store.GetAnnouncement(ctx, d.ann.ID)
		if err != nil {
			d.fail(err)
			continue
		}
		if !ok {
			a.log.Info("the announcement was removed before it played", "name", d.ann.Name)
			continue
		}
		d.ann = ann
		file, next, err := a.announcementFile(ctx, d.ann)
		if err != nil {
			d.fail(err)
			continue
		}
		// The play is written down before it happens. A record that fails
		// after the play would let the same time play again every twenty
		// seconds.
		if err := a.markAnnouncement(ctx, d.ann, d.due, next); err != nil {
			d.fail(err)
			continue
		}
		ready = append(ready, readyAnnouncement{ann: d.ann, file: file, fail: d.fail})
	}
	return ready
}

// queueAnnouncements puts announcements into the queue, each one after the
// entry before it. The queue is the play order, so MPD plays them in that
// order. afterID is the entry the first one goes after, or 0 for the
// current entry.
func (a *App) queueAnnouncements(queued []queuedAnnouncement, ready []readyAnnouncement, afterID int) []queuedAnnouncement {
	for _, r := range ready {
		id, err := a.Player.InsertNext(r.file, afterID)
		if err != nil {
			r.fail(fmt.Errorf("cannot queue %s: %w", r.file, err))
			continue
		}
		afterID = id
		queued = append(queued, queuedAnnouncement{ann: r.ann, id: id})
	}
	return queued
}

// playAnnouncements plays one group of announcements back to back and gives
// the music back after the last one. MPD goes from one to the next with no
// music between them. Announcements asked for while the group plays join
// it.
func (a *App) playAnnouncements(ctx context.Context, list []dueAnnouncement) {
	ready := a.prepareAnnouncements(ctx, list)
	if len(ready) == 0 {
		return
	}

	release := a.Scheduler.Suspend()
	defer release()
	a.announcing.Store(true)
	defer a.announcing.Store(false)

	before, err := a.Player.Status()
	if err != nil {
		for _, r := range ready {
			r.fail(err)
		}
		return
	}
	queued := a.queueAnnouncements(nil, ready, 0)
	if len(queued) == 0 {
		return
	}
	// From here the music must come back, whatever happens. The group can
	// grow while it plays, so the restore reads the list at the end.
	defer func() { a.restoreAfterAnnouncement(ctx, before, queued) }()

	// With repeat on, MPD wraps to the first entry when the last
	// announcement of the queue ends. It must stop instead, and wait for
	// the music to come back.
	if before.Repeat {
		if err := a.Player.SetRepeat(false); err != nil {
			a.log.Warn("cannot stop the repeat for the announcement", "error", err)
		}
	}
	a.holdMusicForAnnouncement(ctx, before)
	a.setAnnouncementLevel(before, queued[0].ann)
	if err := a.Player.PlayID(queued[0].id); err != nil {
		for _, r := range ready {
			r.fail(fmt.Errorf("cannot play the announcement: %w", err))
		}
		return
	}
	a.Events.Publish(events.Player, "")
	var played map[int]bool
	queued, played = a.waitForAnnouncements(ctx, before, queued)
	for _, q := range queued {
		if played[q.id] {
			a.Alerter.Clear(ctx, announcementAlert(q.ann))
		}
	}
	a.Events.Publish(events.Schedule, "")
}

// holdMusicForAnnouncement fades the music out and holds it where it is.
// With no music it does nothing.
func (a *App) holdMusicForAnnouncement(ctx context.Context, before player.Status) {
	if before.State != "play" {
		return
	}
	if before.Volume > 0 {
		scheduler.Fade(ctx, a.Player.SetVolumeRaw, before.Volume, 0, a.Settings().FadeOut)
	}
	// The track keeps its position while the announcement plays.
	if err := a.Player.Pause(); err != nil {
		a.log.Warn("cannot hold the music for the announcement", "error", err)
	}
}

// setAnnouncementLevel sets the level of one announcement.
func (a *App) setAnnouncementLevel(before player.Status, ann store.Announcement) {
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

// waitForAnnouncements returns when the current entry is none of the
// announcements, when playback stops, or when one announcement plays longer
// than the longest hold. When MPD goes on to the next announcement, it sets
// the level of that one. Announcements asked for meanwhile go to the end of
// the group. It returns the group and the queue ids that were seen to
// play. MPD skips a file it cannot read, so a skipped entry is not in it.
func (a *App) waitForAnnouncements(ctx context.Context, before player.Status, queued []queuedAnnouncement) ([]queuedAnnouncement, map[int]bool) {
	started := 1
	played := map[int]bool{}
	deadline := time.Now().Add(announceMax)
	for {
		select {
		case <-ctx.Done():
			return queued, played
		case <-time.After(250 * time.Millisecond):
		}
		st, err := a.Player.Status()
		if err != nil || st.Song == nil || st.State == "stop" {
			return queued, played
		}
		i := slices.IndexFunc(queued, func(q queuedAnnouncement) bool { return q.id == st.Song.ID })
		if i >= 0 {
			played[st.Song.ID] = true
		}
		if more := a.takeAnnouncements(false); len(more) > 0 {
			// The new ones go after the last one of the group. When a
			// music track is current, they go after that track.
			afterID := 0
			if i >= 0 {
				afterID = queued[len(queued)-1].id
			}
			queued = a.queueAnnouncements(queued, a.prepareAnnouncements(ctx, more), afterID)
		}
		switch {
		case i < 0 && started == len(queued):
			return queued, played
		case i < 0:
			// MPD went to a music track, not to the next announcement.
			// Start that announcement.
			i = started
			a.setAnnouncementLevel(before, queued[i].ann)
			if err := a.Player.PlayID(queued[i].id); err != nil {
				a.log.Warn("cannot play the next announcement", "name", queued[i].ann.Name, "error", err)
				return queued, played
			}
		case i >= started:
			a.setAnnouncementLevel(before, queued[i].ann)
		}
		if i >= started {
			started = i + 1
			deadline = time.Now().Add(announceMax)
			a.Events.Publish(events.Player, "")
		}
		if time.Now().After(deadline) {
			a.log.Warn("the announcement plays too long, the music comes back", "id", st.Song.ID)
			return queued, played
		}
	}
}

// restoreAfterAnnouncement removes the announcements from the queue and
// puts the music back where it was. Music that played fades in again.
func (a *App) restoreAfterAnnouncement(ctx context.Context, before player.Status, queued []queuedAnnouncement) {
	// The music must come back even when the request that started this is
	// gone, so the fade does not follow the caller's context.
	ctx = context.WithoutCancel(ctx)
	set := a.Settings()
	if before.Repeat {
		if err := a.Player.SetRepeat(true); err != nil {
			a.log.Warn("cannot start the repeat again", "error", err)
		}
	}
	// The last one goes first. When the current entry goes out of the
	// queue, MPD plays the next one, which must not be an announcement.
	for _, q := range slices.Backward(queued) {
		if err := a.Player.Remove(q.id); err != nil {
			a.log.Debug("the announcement was already out of the queue", "error", err)
		}
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
