package library

import (
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/fhs/gompd/v2/mpd"

	"jukem/internal/mpdctl"
	"jukem/internal/player"
)

// PageSize is the number of entries per browse page.
const PageSize = 200

// Entry is a folder or a track as MPD's database knows it.
type Entry struct {
	Type     string  `json:"type" enum:"directory,file"`
	Path     string  `json:"path" doc:"Relative to the music root"`
	Name     string  `json:"name" doc:"Last path segment"`
	Title    string  `json:"title,omitempty"`
	Artist   string  `json:"artist,omitempty"`
	Album    string  `json:"album,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// Page is one page of a folder listing.
type Page struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
	Total   int     `json:"total" doc:"Entries in the folder"`
	Page    int     `json:"page"`
	Pages   int     `json:"pages"`
}

// Browser reads MPD's database.
type Browser struct {
	pool *mpdctl.Pool
}

// NewBrowser creates a browser over the pool.
func NewBrowser(pool *mpdctl.Pool) *Browser {
	return &Browser{pool: pool}
}

func entryFrom(a mpd.Attrs) (Entry, bool) {
	if d := a["directory"]; d != "" {
		return Entry{Type: "directory", Path: d, Name: path.Base(d)}, true
	}
	if a["file"] == "" {
		return Entry{}, false
	}
	t := player.TrackFrom(a)
	return Entry{Type: "file", Path: t.File, Name: path.Base(t.File), Title: t.Title, Artist: t.Artist, Album: t.Album, Duration: t.Duration}, true
}

// Browse lists one folder in pages of PageSize. Subfolders come first,
// then tracks. Both are in name order without regard to case.
func (b *Browser) Browse(rel string, page int) (Page, error) {
	rel, err := CleanRel(rel)
	if err != nil {
		return Page{}, err
	}
	type keyed struct {
		Entry
		key string
	}
	var entries []keyed
	err = b.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.ListInfo(rel)
		if err != nil {
			return err
		}
		for _, a := range attrs {
			if e, ok := entryFrom(a); ok {
				entries = append(entries, keyed{Entry: e, key: strings.ToLower(e.Name)})
			}
		}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "directory"
		}
		return entries[i].key < entries[j].key
	})
	total := len(entries)
	pages := max(1, (total+PageSize-1)/PageSize)
	page = max(0, min(page, pages-1))
	start := page * PageSize
	end := min(start+PageSize, total)
	out := Page{Path: rel, Entries: make([]Entry, 0, end-start), Total: total, Page: page, Pages: pages}
	for _, e := range entries[start:end] {
		out.Entries = append(out.Entries, e.Entry)
	}
	return out, nil
}

// SearchLimit caps search results.
const SearchLimit = 500

// Search finds tracks whose tags or file name contain q, without regard
// to case. limited is true when MPD had more matches than the limit.
// Each MPD search is bounded with a window, so a broad query on a large
// library does not transfer the whole database.
func (b *Browser) Search(q string) (entries []Entry, limited bool, err error) {
	q = strings.TrimSpace(q)
	entries = []Entry{}
	if q == "" {
		return entries, false, nil
	}
	seen := map[string]bool{}
	window := "0:" + strconv.Itoa(SearchLimit+1)
	err = b.pool.Do(func(c *mpd.Client) error {
		for _, field := range []string{"any", "file"} {
			attrs, err := c.Search(field, q, "window", window)
			if err != nil {
				return err
			}
			for _, a := range attrs {
				e, ok := entryFrom(a)
				if !ok || seen[e.Path] {
					continue
				}
				if len(entries) >= SearchLimit {
					limited = true
					return nil
				}
				seen[e.Path] = true
				entries = append(entries, e)
			}
		}
		return nil
	})
	return entries, limited, err
}
