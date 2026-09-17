package app

import (
	"testing"
	"time"

	"jukem/internal/store"
)

func TestAnnouncementRequests(t *testing.T) {
	a := &App{}
	at := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	one := dueAnnouncement{ann: store.Announcement{ID: 1}, due: at}
	two := dueAnnouncement{ann: store.Announcement{ID: 2}, due: at}

	// The first request starts a group. A request while it plays only
	// waits for the group to take it.
	if !a.addAnnouncements([]dueAnnouncement{one}) {
		t.Fatal("the first request must start a group")
	}
	if a.addAnnouncements([]dueAnnouncement{two}) {
		t.Fatal("a second group must not start while one plays")
	}
	// The next check sees the same times again before they are written
	// down. They must not be added twice.
	a.addAnnouncements([]dueAnnouncement{one, two})
	if got := a.takeAnnouncements(false); len(got) != 2 {
		t.Fatalf("waiting %d, want 2", len(got))
	}

	// A test play has no time, so it is never a duplicate.
	test := dueAnnouncement{ann: store.Announcement{ID: 1}}
	a.addAnnouncements([]dueAnnouncement{test, test})
	if got := a.takeAnnouncements(false); len(got) != 2 {
		t.Fatalf("test plays %d, want 2", len(got))
	}

	// A time that failed can be asked for again.
	a.forgetAnnouncement(1, at)
	a.addAnnouncements([]dueAnnouncement{one})
	if got := a.takeAnnouncements(false); len(got) != 1 {
		t.Fatalf("after a failure %d, want 1", len(got))
	}

	// The group ends only when nothing waits. Then a request starts a new
	// group.
	if got := a.takeAnnouncements(true); len(got) != 0 || a.announceRunning {
		t.Fatal("the group must end when nothing waits")
	}
	later := dueAnnouncement{ann: store.Announcement{ID: 1}, due: at.Add(30 * time.Minute)}
	if !a.addAnnouncements([]dueAnnouncement{later}) {
		t.Fatal("a later time must start a new group")
	}
}
