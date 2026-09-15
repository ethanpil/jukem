// Package player drives MPD's queue and transport, and records the intent
// behind every command so a stopped player can be explained.
package player

import (
	"errors"
	"fmt"
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
	ID       int     `json:"id,omitempty" doc:"Queue entry id, stable while the entry exists"`
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

// Intent is the last command jukem sent, against which queue generation,
// and when.
type Intent struct {
	Kind       IntentKind
	Generation int64
	At         time.Time
	// LastFile is the last file MPD was playing, for the finished check.
	LastFile string
	LastPos  int
}

// Player wraps the MPD pool.
type Player struct {
	pool *mpdctl.Pool

	mu          sync.Mutex
	generation  int64
	intent      Intent
	prioritized map[int]bool // queue ids given priority by Play Next
	limits      func() (min, max int)
}

// New creates a player. limits returns the configured volume floor and
// ceiling.
func New(pool *mpdctl.Pool, limits func() (min, max int)) *Player {
	return &Player{pool: pool, prioritized: map[int]bool{}, limits: limits}
}

// Pool returns the underlying pool, for components with their own commands.
func (p *Player) Pool() *mpdctl.Pool { return p.pool }

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

func (p *Player) record(kind IntentKind) {
	p.mu.Lock()
	p.intent = Intent{Kind: kind, Generation: p.generation, At: time.Now(), LastFile: p.intent.LastFile, LastPos: p.intent.LastPos}
	p.mu.Unlock()
}

// NoteSong remembers the current track, so a later stop can be classified
// as finished when it was the last one.
func (p *Player) NoteSong(file string, pos int) {
	p.mu.Lock()
	p.intent.LastFile = file
	p.intent.LastPos = pos
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
		if st.State != "stop" || attrs["song"] != "" {
			song, err := c.CurrentSong()
			if err != nil {
				return err
			}
			if song["file"] != "" {
				t := trackFrom(song)
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
	st.Error = a["error"]
	st.Updating = a["updating_db"] != ""
	return st
}

func trackFrom(a mpd.Attrs) Track {
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
		t.Title = baseName(t.File)
	}
	return t
}

func baseName(file string) string {
	if i := strings.LastIndex(file, "/"); i >= 0 {
		file = file[i+1:]
	}
	if i := strings.LastIndex(file, "."); i > 0 {
		file = file[:i]
	}
	return file
}

// Play starts playback of the current position.
func (p *Player) Play() error {
	p.record(IntentPlay)
	return p.pool.Do(func(c *mpd.Client) error { return c.Play(-1) })
}

// PlayID starts playback at a queue entry.
func (p *Player) PlayID(id int) error {
	p.record(IntentPlay)
	return p.pool.Do(func(c *mpd.Client) error { return c.PlayID(id) })
}

// Pause pauses playback.
func (p *Player) Pause() error {
	p.record(IntentPause)
	return p.pool.Do(func(c *mpd.Client) error { return c.Pause(true) })
}

// Stop stops playback.
func (p *Player) Stop() error {
	p.record(IntentStop)
	return p.pool.Do(func(c *mpd.Client) error { return c.Stop() })
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

// SetRepeat turns repeat on or off. Scheduled playback keeps it on.
func (p *Player) SetRepeat(on bool) error {
	return p.pool.Do(func(c *mpd.Client) error { return c.Repeat(on) })
}

// SetCrossfade sets the crossfade in seconds.
func (p *Player) SetCrossfade(seconds int) error {
	return p.pool.Do(func(c *mpd.Client) error {
		return c.Command("crossfade %d", seconds).OK()
	})
}

// Queue returns one page of the queue.
func (p *Player) Queue(offset, limit int) ([]Track, int, error) {
	var tracks []Track
	total := 0
	err := p.pool.Do(func(c *mpd.Client) error {
		st, err := c.Status()
		if err != nil {
			return err
		}
		total, _ = strconv.Atoi(st["playlistlength"])
		if offset >= total {
			tracks = []Track{}
			return nil
		}
		end := min(offset+limit, total)
		attrs, err := c.PlaylistInfo(offset, end)
		if err != nil {
			return err
		}
		tracks = make([]Track, 0, len(attrs))
		for _, a := range attrs {
			tracks = append(tracks, trackFrom(a))
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

// Clear empties the queue and starts a new generation.
func (p *Player) Clear() error {
	err := p.pool.Do(func(c *mpd.Client) error { return c.Clear() })
	if err == nil {
		p.newGeneration()
	}
	return err
}

func (p *Player) newGeneration() {
	p.mu.Lock()
	p.generation++
	p.prioritized = map[int]bool{}
	p.mu.Unlock()
}

// Load replaces the queue with files, in order, and starts playing. It
// returns the number of tracks loaded, at most MaxQueue.
func (p *Player) Load(files []string, shuffle bool, volume int) (int, error) {
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
		if volume >= 0 {
			if err := c.SetVolume(volume); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	p.newGeneration()
	p.record(IntentPlay)
	return len(files), p.pool.Do(func(c *mpd.Client) error { return c.Play(-1) })
}

// addAll adds files in a command list, in order.
func addAll(c *mpd.Client, files []string) error {
	const batch = 500
	for i := 0; i < len(files); i += batch {
		cl := c.BeginCommandList()
		for _, f := range files[i:min(i+batch, len(files))] {
			cl.Add(f)
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
		st, err := c.Status()
		if err != nil {
			return err
		}
		length, _ := strconv.Atoi(st["playlistlength"])
		if length+len(files) > MaxQueue {
			files = files[:max(0, MaxQueue-length)]
		}
		playing := st["state"] != "stop" && st["song"] != ""
		for i, f := range files {
			var attrs mpd.Attrs
			if playing {
				// Relative positions keep the selection in order.
				attrs, err = c.Command("addid %s +%d", f, i).Attrs()
			} else {
				attrs, err = c.Command("addid %s %d", f, i).Attrs()
			}
			if err != nil {
				return err
			}
			id, _ := strconv.Atoi(attrs["Id"])
			ids = append(ids, id)
		}
		if st["random"] == "1" {
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
		st, err := c.Status()
		if err != nil {
			return err
		}
		length, _ := strconv.Atoi(st["playlistlength"])
		if length+len(files) > MaxQueue {
			files = files[:max(0, MaxQueue-length)]
		}
		return addAll(c, files)
	})
	return len(files), err
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
// order. dir "" is the whole library.
func (p *Player) ListFiles(dir string) ([]string, error) {
	var files []string
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.ListAllInfo(dir)
		if err != nil {
			return err
		}
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
	sort.SliceStable(files, func(i, j int) bool {
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	return files, nil
}

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

// Ping reports whether MPD answers.
func (p *Player) Ping() error {
	return p.pool.Do(func(c *mpd.Client) error { return c.Ping() })
}

// Finished reports whether a stopped MPD reached the end of its queue on
// purpose: the last intent was play, the generation is unchanged, MPD
// reports no error, and the last track was the final one.
func (p *Player) Finished(st Status) bool {
	in := p.LastIntent()
	if in.Kind != IntentPlay || in.Generation != p.Generation() || st.Error != "" {
		return false
	}
	return st.State == "stop" && st.QueueLength > 0 && in.LastPos == st.QueueLength-1
}

// Describe returns a short text for logs.
func (st Status) Describe() string {
	if st.Song == nil {
		return st.State
	}
	return fmt.Sprintf("%s %s", st.State, st.Song.File)
}
