package player

import (
	"testing"

	"github.com/fhs/gompd/v2/mpd"
)

func TestParseStatus(t *testing.T) {
	st := parseStatus(mpd.Attrs{"state": "play", "volume": "42", "elapsed": "12.5", "random": "1", "playlistlength": "7", "playlist": "99", "updating_db": "3", "nextsong": "3"})
	if st.State != "play" || st.Volume != 42 || st.Elapsed != 12.5 || !st.Shuffle || st.QueueLength != 7 || st.QueueVersion != 99 || !st.Updating || !st.HasNext {
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
	p := New(nil, func() (int, int) { return 0, 100 })
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
