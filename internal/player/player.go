// Package player drives MPD's queue and transport. It records the intent
// behind every command, so that a stopped player has an explanation.
package player

import (
	"errors"
	"math/rand/v2"
	"path"
	"slices"
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
}

// Status is the transport state MPD reports.
type Status struct {
	State   string  `json:"state" enum:"play,pause,stop"`
	Song    *Track  `json:"song,omitempty" doc:"Current queue entry"`
	Elapsed float64 `json:"elapsed" doc:"Seconds into the current track"`
	Volume  int     `json:"volume" minimum:"-1" maximum:"100" doc:"-1 when MPD has no mixer"`
	Shuffle bool    `json:"shuffle" doc:"True when the queue plays in a shuffled order"`
	Repeat  bool    `json:"repeat"`
	// Random is MPD's random mode. jukem keeps it off: a shuffle puts the
	// queue itself in a random order, so the queue shows the play order.
	Random       bool   `json:"-"`
	QueueLength  int    `json:"queue_length"`
	QueueVersion int    `json:"queue_version" doc:"Changes whenever the queue changes"`
	HasNext      bool   `json:"-"`
	Error        string `json:"error,omitempty" doc:"MPD's last error, if any"`
	Updating     bool   `json:"updating" doc:"True while MPD scans the library"`
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

	mu         sync.Mutex
	generation int64
	intent     Intent
	limits     func() (min, max int)
	persist    func(generation int64)

	// shuffle is on when the queue plays in a shuffled order. order holds
	// the positions of each file in the list that Load received. A
	// shuffle that goes off uses order to put the queue back. order is
	// empty after a restart. Then the order is by file name.
	shuffle        bool
	persistShuffle func(on bool)
	order          map[string][]int
}

// New creates a player. limits returns the configured volume floor and
// ceiling. generation is the queue generation from before the restart,
// and persist stores each new one, so an override made against the queue
// stays valid after a restart.
func New(pool *mpdctl.Pool, limits func() (min, max int), generation int64, persist func(int64)) *Player {
	return &Player{pool: pool, limits: limits, generation: generation, persist: persist}
}

// UseShuffleState sets the shuffle state from before the restart, and the
// function that stores each change.
func (p *Player) UseShuffleState(on bool, persist func(bool)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shuffle, p.persistShuffle = on, persist
}

// Shuffle reports whether the queue plays in a shuffled order.
func (p *Player) Shuffle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shuffle
}

// setShuffle records the shuffle state and stores a change. The store
// write is outside the lock, because it can be slow.
func (p *Player) setShuffle(on bool) {
	p.mu.Lock()
	changed := p.shuffle != on
	p.shuffle = on
	persist := p.persistShuffle
	p.mu.Unlock()
	if changed && persist != nil {
		persist(on)
	}
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
		st.Shuffle = p.Shuffle()
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
	st.Random = a["random"] == "1"
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

// SetShuffle turns the shuffle on or off. The tracks after the current
// one change order, and the current track keeps playing. On shuffles them.
// Off puts them back in the order they were loaded, or in file name order
// after a restart.
func (p *Player) SetShuffle(on bool) error {
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		if attrs["random"] == "1" {
			if err := c.Random(false); err != nil {
				return err
			}
		}
		start := 0
		if pos, err := strconv.Atoi(attrs["song"]); err == nil {
			start = pos + 1
		}
		end, _ := strconv.Atoi(attrs["playlistlength"])
		if end-start < 2 {
			return nil
		}
		if on {
			return c.Command("shuffle %d:%d", start, end).OK()
		}
		attrsList, err := c.PlaylistInfo(start, end)
		if err != nil {
			return err
		}
		tracks := make([]Track, len(attrsList))
		for i, a := range attrsList {
			tracks[i] = TrackFrom(a)
		}
		p.mu.Lock()
		ids := loadedOrder(tracks, p.order)
		p.mu.Unlock()
		// A long command list can pass the deadline of the pool, so the
		// moves go in batches, like the adds of a load.
		const batch = 500
		for i := 0; i < len(ids); i += batch {
			cl := c.BeginCommandList()
			for j, id := range ids[i:min(i+batch, len(ids))] {
				cl.MoveID(id, start+i+j)
			}
			if err := cl.End(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	p.setShuffle(on)
	return nil
}

// loadedOrder returns the queue ids of tracks in the order of the list that
// Load received. A file that is in that list more than once gets its
// places in turn. A track that is not in the list keeps its place after
// the track before it. With no list, the order is by file name.
func loadedOrder(tracks []Track, order map[string][]int) []int {
	type keyed struct {
		id  int
		key string
		idx int
	}
	used := make(map[string]int, len(order))
	list := make([]keyed, len(tracks))
	last := -1
	for i, t := range tracks {
		idx := last
		if places := order[t.File]; len(places) > 0 {
			idx = places[min(used[t.File], len(places)-1)]
			used[t.File]++
			last = idx
		}
		list[i] = keyed{id: t.ID, key: strings.ToLower(t.File), idx: idx}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if len(order) == 0 {
			return list[i].key < list[j].key
		}
		return list[i].idx < list[j].idx
	})
	ids := make([]int, len(list))
	for i, k := range list {
		ids[i] = k.id
	}
	return ids
}

// ReshuffleAtEnd gives a repeating shuffled queue a new order for its next
// pass. It runs when the last track starts. The other tracks are shuffled,
// and the track that plays becomes the first entry. It keeps playing, and
// the next pass starts with the entry after it. So no track stays at the
// end of the queue.
func (p *Player) ReshuffleAtEnd(st Status) error {
	if !p.Shuffle() || !st.Repeat || st.Song == nil || st.Song.Pos != st.QueueLength-1 || st.Song.Pos < 2 {
		return nil
	}
	id := st.Song.ID
	return p.pool.Do(func(c *mpd.Client) error {
		// The queue is read again here: it can have changed since the
		// event that started this.
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		pos, err := strconv.Atoi(attrs["song"])
		length, _ := strconv.Atoi(attrs["playlistlength"])
		if err != nil || pos != length-1 || pos < 2 {
			return nil
		}
		if err := c.Command("shuffle %d:%d", 0, pos).OK(); err != nil {
			return err
		}
		return c.MoveID(id, 0)
	})
}

// SetRepeat turns MPD's repeat on or off.
func (p *Player) SetRepeat(on bool) error {
	return p.pool.Do(func(c *mpd.Client) error { return c.Repeat(on) })
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
	p.mu.Unlock()
}

// Load replaces the queue with files, sets the options, and starts
// playing. With shuffle the queue is in a random order, otherwise in the
// order of files. It returns the number of tracks loaded, at most
// MaxQueue. A negative volume leaves the volume alone. Scheduled programs
// repeat until their window ends; a Play Now selection plays once, so it
// can finish.
func (p *Player) Load(files []string, shuffle bool, volume int, repeat bool) (int, error) {
	if len(files) > MaxQueue {
		files = files[:MaxQueue]
	}
	order := make(map[string][]int, len(files))
	for i, f := range files {
		order[f] = append(order[f], i)
	}
	queue := files
	if shuffle {
		queue = slices.Clone(files)
		rand.Shuffle(len(queue), func(i, j int) { queue[i], queue[j] = queue[j], queue[i] })
	}
	err := p.pool.Do(func(c *mpd.Client) error {
		if err := c.Clear(); err != nil {
			return err
		}
		if err := addAll(c, queue); err != nil {
			return err
		}
		if err := c.Random(false); err != nil {
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
	p.mu.Lock()
	p.order = order
	p.mu.Unlock()
	p.setShuffle(shuffle)
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

// PlayNext inserts files directly after the current track, in order. The
// queue is the play order, also with shuffle on, so they play next.
// Nothing starts playing.
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
		return nil
	})
	return len(ids), err
}

// Add puts files into the queue. Nothing starts playing. With shuffle on
// each file goes to a place of its own after the current track, so it can
// play soon, like a track added to a shuffled queue before. Otherwise the
// files go to the end.
func (p *Player) Add(files []string) (int, error) {
	if len(files) == 0 {
		return 0, nil
	}
	shuffle := p.Shuffle()
	err := p.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.Status()
		if err != nil {
			return err
		}
		length := parseStatus(attrs).QueueLength
		if length+len(files) > MaxQueue {
			files = files[:max(0, MaxQueue-length)]
		}
		if !shuffle {
			return addAll(c, files)
		}
		first := 0
		if pos, err := strconv.Atoi(attrs["song"]); err == nil {
			first = pos + 1
		}
		for _, f := range files {
			at := length
			if length > first {
				at = first + rand.IntN(length-first+1)
			}
			if _, err := c.Command("addid %s %d", literal(f), at).Attrs(); err != nil {
				return err
			}
			length++
		}
		return nil
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
