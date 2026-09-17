package player

import (
	"testing"

	"github.com/fhs/gompd/v2/mpd"
)

func TestParseStatus(t *testing.T) {
	st := parseStatus(mpd.Attrs{"state": "play", "volume": "42", "elapsed": "12.5", "random": "1", "playlistlength": "7", "playlist": "99", "updating_db": "3", "nextsong": "3"})
	if st.State != "play" || st.Volume != 42 || st.Elapsed != 12.5 || !st.Random || st.QueueLength != 7 || st.QueueVersion != 99 || !st.Updating || !st.HasNext {
		t.Fatalf("got %+v", st)
	}
	if st := parseStatus(mpd.Attrs{"state": "stop"}); st.Volume != -1 || st.Updating || st.HasNext {
		t.Fatalf("got %+v", st)
	}
}

func TestTrackFrom(t *testing.T) {
	tr := TrackFrom(mpd.Attrs{"file": "Rock/Band/01 - Song.flac", "Id": "5", "Pos": "2", "duration": "200.5"})
	if tr.Title != "01 - Song" || tr.ID != 5 || tr.Pos != 2 || tr.Duration != 200.5 {
		t.Fatalf("got %+v", tr)
	}
	tr = TrackFrom(mpd.Attrs{"file": "a.mp3", "Title": "A", "Time": "30"})
	if tr.Title != "A" || tr.Duration != 30 {
		t.Fatalf("got %+v", tr)
	}
	if titleFromFile(".hidden") != ".hidden" || titleFromFile("dir/noext") != "noext" {
		t.Fatal("title edge cases")
	}
}

func TestFinished(t *testing.T) {
	p := New(nil, func() (int, int) { return 0, 100 }, 0, nil)
	p.record(IntentPlay)
	// The final track of a shuffled queue is the one with no successor,
	// whatever its position.
	p.NoteSong(Status{Song: &Track{File: "x.mp3", Pos: 1}, HasNext: false})
	if !p.Finished(Status{State: "stop", QueueLength: 5}) {
		t.Fatal("expected finished")
	}
	if p.Finished(Status{State: "stop", QueueLength: 5, Error: "device busy"}) {
		t.Fatal("error is a failure")
	}
	p.NoteSong(Status{Song: &Track{File: "y.mp3", Pos: 2}, HasNext: true})
	if p.Finished(Status{State: "stop", QueueLength: 5}) {
		t.Fatal("a track with a successor is not final")
	}
	p.NoteSong(Status{Song: &Track{File: "z.mp3"}, HasNext: false})
	p.record(IntentStop)
	if p.Finished(Status{State: "stop", QueueLength: 5}) {
		t.Fatal("stop intent is not finished")
	}
	p.record(IntentPlay)
	p.newGeneration()
	if p.Finished(Status{State: "stop", QueueLength: 5}) {
		t.Fatal("generation changed after the intent")
	}
}

func TestLoadedOrder(t *testing.T) {
	tracks := []Track{
		{ID: 7, File: "b.mp3"},
		{ID: 3, File: "a.mp3"},
		{ID: 9, File: "new.mp3"},
		{ID: 5, File: "c.mp3"},
	}
	// With the loaded order the tracks go back to it. A file that the load
	// did not bring keeps its place after the track before it.
	order := map[string][]int{"a.mp3": {0}, "b.mp3": {1}, "c.mp3": {2}}
	// new.mp3 follows a.mp3, the track before it in the queue.
	got := loadedOrder(tracks, order)
	want := []int{3, 9, 7, 5}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("with the loaded order: %v, want %v", got, want)
		}
	}
	// A file that the load had twice gets both of its places.
	twice := []Track{{ID: 1, File: "a.mp3"}, {ID: 2, File: "b.mp3"}, {ID: 3, File: "a.mp3"}}
	got = loadedOrder(twice, map[string][]int{"a.mp3": {0, 2}, "b.mp3": {1}})
	want = []int{1, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("a file that is in the list twice: %v, want %v", got, want)
		}
	}

	// After a restart there is no loaded order, so the order is by name.
	got = loadedOrder(tracks, nil)
	want = []int{3, 7, 5, 9}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("by name: %v, want %v", got, want)
		}
	}
}

func TestReshuffleAtEndOnlyForTheLastTrack(t *testing.T) {
	p := New(nil, func() (int, int) { return 0, 100 }, 0, nil)
	// The pool is nil, so a call that reaches MPD panics. These calls must
	// all stop before that.
	p.UseShuffleState(false, nil)
	if err := p.ReshuffleAtEnd(Status{Repeat: true, QueueLength: 5, Song: &Track{Pos: 4}}); err != nil {
		t.Fatal(err)
	}
	p.UseShuffleState(true, nil)
	for _, st := range []Status{
		{Repeat: false, QueueLength: 5, Song: &Track{Pos: 4}}, // no repeat
		{Repeat: true, QueueLength: 5, Song: &Track{Pos: 2}},  // not the last
		{Repeat: true, QueueLength: 5},                        // nothing plays
		{Repeat: true, QueueLength: 2, Song: &Track{Pos: 1}},  // too short
	} {
		if err := p.ReshuffleAtEnd(st); err != nil {
			t.Fatalf("%+v: %v", st, err)
		}
	}
}
