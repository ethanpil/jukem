package player

import (
	"testing"

	"github.com/fhs/gompd/v2/mpd"
)

func TestParseStatus(t *testing.T) {
	st := parseStatus(mpd.Attrs{"state": "play", "volume": "42", "elapsed": "12.5", "random": "1", "playlistlength": "7", "playlist": "99", "updating_db": "3"})
	if st.State != "play" || st.Volume != 42 || st.Elapsed != 12.5 || !st.Shuffle || st.QueueLength != 7 || st.QueueVersion != 99 || !st.Updating {
		t.Fatalf("got %+v", st)
	}
	if st := parseStatus(mpd.Attrs{"state": "stop"}); st.Volume != -1 || st.Updating {
		t.Fatalf("got %+v", st)
	}
}

func TestTrackFrom(t *testing.T) {
	tr := trackFrom(mpd.Attrs{"file": "Rock/Band/01 - Song.flac", "Id": "5", "Pos": "2", "duration": "200.5"})
	if tr.Title != "01 - Song" || tr.ID != 5 || tr.Pos != 2 || tr.Duration != 200.5 {
		t.Fatalf("got %+v", tr)
	}
	tr = trackFrom(mpd.Attrs{"file": "a.mp3", "Title": "A", "Time": "30"})
	if tr.Title != "A" || tr.Duration != 30 {
		t.Fatalf("got %+v", tr)
	}
}

func TestFinished(t *testing.T) {
	p := New(nil, func() (int, int) { return 0, 100 })
	p.record(IntentPlay)
	p.NoteSong("last.mp3", 4)
	if !p.Finished(Status{State: "stop", QueueLength: 5}) {
		t.Fatal("expected finished")
	}
	if p.Finished(Status{State: "stop", QueueLength: 5, Error: "device busy"}) {
		t.Fatal("error is a failure")
	}
	if p.Finished(Status{State: "stop", QueueLength: 9}) {
		t.Fatal("not the last track")
	}
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
