// Package player drives MPD's queue and transport. It records the intent
// behind every command, so that a stopped player has an explanation.
package player

import (
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fhs/gompd/v2/mpd"

	"jukem/internal/mpdctl"
)

// MaxQueue is the supported queue ceiling, matching max_playlist_length.
const MaxQueue = 20000

// Track is one queue entry or library file.
type Track struct {
	ID       int     `json:"id" doc:"Queue entry id, stable while the entry exists"`
	Pos      int     `json:"pos" doc:"Position in the queue, from 0"`
	File     string  `json:"file" doc:"Path relative to the music root"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist,omitempty"`
	Album    string  `json:"album,omitempty"`
	Duration float64 `json:"duration,omitempty" doc:"Length in seconds"`
	Prio     int     `json:"prio,omitempty" doc:"MPD priority; 255 plays next when shuffle is on"`
}

// Status is the transport state MPD reports.
type Status struct {
	State        string  `json:"state" enum:"play,pause,stop"`
	Song         *Track  `json:"song,omitempty" doc:"Current queue entry"`
	Elapsed      float64 `json:"elapsed" doc:"Seconds into the current track"`
	Volume       int     `json:"volume" minimum:"-1" maximum:"100" doc:"-1 when MPD has no mixer"`
	Shuffle      bool    `json:"shuffle"`
	Repeat       bool    `json:"repeat"`
	QueueLength  int     `json:"queue_length"`
	QueueVersion int     `json:"queue_version" doc:"Changes whenever the queue changes"`
	HasNext      bool    `json:"-"`
	Error        string  `json:"error,omitempty" doc:"MPD's last error, if any"`
	Updating     bool    `json:"updating" doc:"True while MPD scans the library"`
}

// IntentKind says what jukem last asked MPD to do.
type IntentKind string

const (
	IntentNone  IntentKind = ""
	IntentPlay  IntentKind = "play"
	IntentPause IntentKind = "pause"
	IntentStop  IntentKind = "stop"
)

// Intent is the last command jukem sent. It carries the queue generation
// at that time and the last track MPD played.
type Intent struct {
	Kind       IntentKind
	Generation int64
	// LastFile is the last file MPD played, and LastWasFinal says whether
	// MPD had no next track after it.
	LastFile     string
	LastWasFinal bool
}

// Player wraps the MPD pool.
type Player struct {
	pool *mpdctl.Pool

	mu          sync.Mutex
	generation  int64
	intent      Intent
	prioritized map[int]bool // queue ids given priority by Play Next
	limits      func() (min, max int)
	persist     func(generation int64)
}

// New creates a player. limits returns the configured volume floor and
// ceiling. generation is the queue generation from before the restart,
// and persist stores each new one, so an override made against the queue
// stays valid after a restart.
func New(pool *mpdctl.Pool, limits func() (min, max int), generation int64, persist func(int64)) *Player {
	return &Player{pool: pool, prioritized: map[int]bool{}, limits: limits, generation: generation, persist: persist}
}

// Generation returns the queue generation, which increments each time the
// queue is replaced.
func (p *Player) Generation() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation
}

// LastIntent returns the last recorded command.
func (p *Player) LastIntent() Intent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.intent
}

// record stores the intent of a command that MPD accepted.
func (p *Player) record(kind IntentKind) {
	p.mu.Lock()
	p.intent.Kind = kind
	p.intent.Generation = p.generation
	p.mu.Unlock()
}

// NoteSong remembers the current track and whether it is the final one, so
// a later stop is classified as finished only when MPD ran out of tracks.
func (p *Player) NoteSong(st Status) {
	if st.Song == nil {
		return
	}
	p.mu.Lock()
	p.intent.LastFile = st.Song.File
	p.intent.LastWasFinal = !st.HasNext
	p.mu.Unlock()
}

// Status reads MPD's status and current song.
func (p *Player) Status() (Status, error) {
	var st Status
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		st = parseStatus(attrs)
		if attrs["song"] != "" {
			song, err := c.CurrentSong()
			if err != nil {
				return err
			}
			if song["file"] != "" {
				t := TrackFrom(song)
				st.Song = &t
			}
		}
		return nil
	})
	return st, err
}

func parseStatus(a mpd.Attrs) Status {
	st := Status{State: a["state"], Volume: -1}
	if v, err := strconv.Atoi(a["volume"]); err == nil {
		st.Volume = v
	}
	st.Elapsed, _ = strconv.ParseFloat(a["elapsed"], 64)
	st.Shuffle = a["random"] == "1"
	st.Repeat = a["repeat"] == "1"
	st.QueueLength, _ = strconv.Atoi(a["playlistlength"])
	st.QueueVersion, _ = strconv.Atoi(a["playlist"])
	st.HasNext = a["nextsong"] != ""
	st.Error = a["error"]
	st.Updating = a["updating_db"] != ""
	return st
}

// TrackFrom builds a Track from MPD's attributes for a song.
func TrackFrom(a mpd.Attrs) Track {
	t := Track{File: a["file"], Title: a["Title"], Artist: a["Artist"], Album: a["Album"]}
	t.ID, _ = strconv.Atoi(a["Id"])
	t.Pos, _ = strconv.Atoi(a["Pos"])
	t.Prio, _ = strconv.Atoi(a["Prio"])
	if d, err := strconv.ParseFloat(a["duration"], 64); err == nil {
		t.Duration = d
	} else if d, err := strconv.Atoi(a["Time"]); err == nil {
		t.Duration = float64(d)
	}
	if t.Title == "" {
		t.Title = titleFromFile(t.File)
	}
	return t
}

// titleFromFile is the file name without directory and extension.
func titleFromFile(file string) string {
	base := path.Base(file)
	if ext := path.Ext(base); ext != "" && ext != base {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

// transport sends one command and records its intent when MPD accepts it.
func (p *Player) transport(kind IntentKind, fn func(c *mpd.Client) error) error {
	if err := p.pool.Do(fn); err != nil {
		return err
	}
	p.record(kind)
	return nil
}

// Play starts playback of the current position.
func (p *Player) Play() error {
	return p.transport(IntentPlay, func(c *mpd.Client) error { return c.Play(-1) })
}

// PlayID starts playback at a queue entry.
func (p *Player) PlayID(id int) error {
	return p.transport(IntentPlay, func(c *mpd.Client) error { return c.PlayID(id) })
}

// Pause pauses playback.
func (p *Player) Pause() error {
	return p.transport(IntentPause, func(c *mpd.Client) error { return c.Pause(true) })
}

// Stop stops playback.
func (p *Player) Stop() error {
	return p.transport(IntentStop, func(c *mpd.Client) error { return c.Stop() })
}

// Next skips to the next track.
func (p *Player) Next() error {
	return p.pool.Do(func(c *mpd.Client) error { return c.Next() })
}

// Previous goes back one track.
func (p *Player) Previous() error {
	return p.pool.Do(func(c *mpd.Client) error { return c.Previous() })
}

// Seek moves inside the current track.
func (p *Player) Seek(seconds float64) error {
	if seconds < 0 {
		return errors.New("position must be positive")
	}
	return p.pool.Do(func(c *mpd.Client) error {
		return c.SeekCur(time.Duration(seconds*float64(time.Second)), false)
	})
}

// SetVolume sets the software mixer, clamped to the configured limits. It
// returns the volume applied.
func (p *Player) SetVolume(v int) (int, error) {
	lo, hi := p.limits()
	v = max(lo, min(hi, v))
	return v, p.SetVolumeRaw(v)
}

// SetVolumeRaw sets the volume without the limits, for jukem's own fades.
func (p *Player) SetVolumeRaw(v int) error {
	v = max(0, min(100, v))
	return p.pool.Do(func(c *mpd.Client) error { return c.SetVolume(v) })
}

// SetShuffle turns MPD's random mode on or off.
func (p *Player) SetShuffle(on bool) error {
	return p.pool.Do(func(c *mpd.Client) error { return c.Random(on) })
}

// SetCrossfade sets the crossfade in seconds.
func (p *Player) SetCrossfade(seconds int) error {
	return p.pool.Do(func(c *mpd.Client) error {
		return c.Command("crossfade %d", seconds).OK()
	})
}

// Queue returns one page of the queue and the total length.
func (p *Player) Queue(offset, limit int) ([]Track, int, error) {
	tracks := []Track{}
	total := 0
	err := p.pool.Do(func(c *mpd.Client) error {
		st, err := c.Status()
		if err != nil {
			return err
		}
		total = parseStatus(st).QueueLength
		if offset >= total {
			return nil
		}
		attrs, err := c.PlaylistInfo(offset, min(offset+limit, total))
		if err != nil {
			return err
		}
		for _, a := range attrs {
			tracks = append(tracks, TrackFrom(a))
		}
		return nil
	})
	return tracks, total, err
}

// Remove deletes a queue entry by id.
func (p *Player) Remove(id int) error {
	return p.pool.Do(func(c *mpd.Client) error { return c.DeleteID(id) })
}

// Move puts the entry with id at position to.
func (p *Player) Move(id, to int) error {
	return p.pool.Do(func(c *mpd.Client) error { return c.MoveID(id, to) })
}

func (p *Player) newGeneration() {
	p.mu.Lock()
	p.generation++
	if p.persist != nil {
		p.persist(p.generation)
	}
	p.prioritized = map[int]bool{}
	p.mu.Unlock()
}

// Load replaces the queue with files, in order, sets the options, and
// starts playing. It returns the number of tracks loaded, at most
// MaxQueue. A negative volume leaves the volume alone. Scheduled programs
// repeat until their window ends; a Play Now selection plays once, so it
// can finish.
func (p *Player) Load(files []string, shuffle bool, volume int, repeat bool) (int, error) {
	if len(files) > MaxQueue {
		files = files[:MaxQueue]
	}
	err := p.pool.Do(func(c *mpd.Client) error {
		if err := c.Clear(); err != nil {
			return err
		}
		if err := addAll(c, files); err != nil {
			return err
		}
		if err := c.Random(shuffle); err != nil {
			return err
		}
		if err := c.Repeat(repeat); err != nil {
			return err
		}
		if volume >= 0 {
			if err := c.SetVolume(volume); err != nil {
				return err
			}
		}
		return c.Play(-1)
	})
	if err != nil {
		return 0, err
	}
	p.newGeneration()
	p.record(IntentPlay)
	return len(files), nil
}

// addAll adds files in a command list, in order.
func addAll(c *mpd.Client, files []string) error {
	const batch = 500
	for i := 0; i < len(files); i += batch {
		cl := c.BeginCommandList()
		for _, f := range files[i:min(i+batch, len(files))] {
			cl.Add(literal(f))
		}
		if err := cl.End(); err != nil {
			return err
		}
	}
	return nil
}

// PlayNext inserts files directly after the current track, in order. With
// shuffle on they also get the highest priority, so they play before the
// rest of the queue. Nothing starts playing.
func (p *Player) PlayNext(files []string) (int, error) {
	if len(files) == 0 {
		return 0, nil
	}
	var ids []int
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		st := parseStatus(attrs)
		if st.QueueLength+len(files) > MaxQueue {
			files = files[:max(0, MaxQueue-st.QueueLength)]
		}
		// A current entry exists while playing, paused, or stopped inside
		// the queue; the insert goes after it. Otherwise it goes on top.
		hasCurrent := attrs["song"] != ""
		for i, f := range files {
			var a mpd.Attrs
			if hasCurrent {
				a, err = c.Command("addid %s +%d", literal(f), i).Attrs()
			} else {
				a, err = c.Command("addid %s %d", literal(f), i).Attrs()
			}
			if err != nil {
				return err
			}
			id, _ := strconv.Atoi(a["Id"])
			ids = append(ids, id)
		}
		if st.Shuffle {
			for _, id := range ids {
				if err := c.SetPriorityID(255, id); err != nil {
					return err
				}
			}
			p.mu.Lock()
			for _, id := range ids {
				p.prioritized[id] = true
			}
			p.mu.Unlock()
		}
		return nil
	})
	return len(ids), err
}

// Add appends files to the end of the queue. Nothing starts playing.
func (p *Player) Add(files []string) (int, error) {
	if len(files) == 0 {
		return 0, nil
	}
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		length := parseStatus(attrs).QueueLength
		if length+len(files) > MaxQueue {
			files = files[:max(0, MaxQueue-length)]
		}
		return addAll(c, files)
	})
	return len(files), err
}

// RemoveFile takes every queue entry of a file out of the queue.
func (p *Player) RemoveFile(file string) error {
	return p.pool.Do(func(c *mpd.Client) error {
		entries, err := c.Command("playlistfind file %s", literal(file)).AttrsList("file")
		if err != nil {
			return err
		}
		for _, e := range entries {
			id, err := strconv.Atoi(e["Id"])
			if err != nil {
				continue
			}
			if err := c.DeleteID(id); err != nil {
				return err
			}
		}
		return nil
	})
}

// SongChanged resets the priority of a Play Next track once it plays, so
// it does not jump the queue again.
func (p *Player) SongChanged(currentID int) {
	p.mu.Lock()
	if !p.prioritized[currentID] {
		p.mu.Unlock()
		return
	}
	delete(p.prioritized, currentID)
	p.mu.Unlock()
	p.pool.Do(func(c *mpd.Client) error { return c.SetPriorityID(0, currentID) })
}

// ListFiles returns every audio file below dir, in case-insensitive path
// order. dir "" is the whole library. The search answer lists songs only,
// so folders and playlists inside the tree do not break the parse.
func (p *Player) ListFiles(dir string) ([]string, error) {
	var files []string
	err := p.pool.Do(func(c *mpd.Client) error {
		var attrs []mpd.Attrs
		var err error
		if dir == "" {
			attrs, err = c.Search("file", "")
		} else {
			attrs, err = c.Search("base", literal(dir))
		}
		if err != nil {
			return err
		}
		files = make([]string, 0, len(attrs))
		for _, a := range attrs {
			if f := a["file"]; f != "" {
				files = append(files, f)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(files))
	for i, f := range files {
		keys[i] = strings.ToLower(f)
	}
	sort.Stable(byKey{files, keys})
	return files, nil
}

// byKey sorts files by a precomputed key without an allocation per
// comparison.
type byKey struct{ files, keys []string }

func (b byKey) Len() int           { return len(b.files) }
func (b byKey) Less(i, j int) bool { return b.keys[i] < b.keys[j] }
func (b byKey) Swap(i, j int) {
	b.files[i], b.files[j] = b.files[j], b.files[i]
	b.keys[i], b.keys[j] = b.keys[j], b.keys[i]
}

// literal prepares a string for a gompd command that reads an answer:
// gompd sends the assembled command through Fprintf, so a percent sign in
// a file name must be doubled.
func literal(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// Update asks MPD to scan a path ("" for everything). It returns the job id.
func (p *Player) Update(path string) (int, error) {
	var job int
	err := p.pool.Do(func(c *mpd.Client) error {
		var err error
		job, err = c.Update(path)
		return err
	})
	return job, err
}

// Stats reports the number of songs MPD knows and when its database was
// last updated.
func (p *Player) Stats() (songs int, dbUpdate time.Time, err error) {
	err = p.pool.Do(func(c *mpd.Client) error {
		a, err := c.Stats()
		if err != nil {
			return err
		}
		songs, _ = strconv.Atoi(a["songs"])
		if sec, err := strconv.ParseInt(a["db_update"], 10, 64); err == nil && sec > 0 {
			dbUpdate = time.Unix(sec, 0)
		}
		return nil
	})
	return songs, dbUpdate, err
}

// Finished reports whether a stopped MPD reached the end of its queue. That
// is the case when the last intent was play against the same generation,
// MPD reports no error, and the last track played had no successor.
func (p *Player) Finished(st Status) bool {
	in := p.LastIntent()
	if in.Kind != IntentPlay || in.Generation != p.Generation() || st.Error != "" {
		return false
	}
	return st.State == "stop" && st.QueueLength > 0 && in.LastWasFinal
}
