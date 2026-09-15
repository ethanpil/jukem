package library

import (
	"sort"
	"strconv"
	"strings"

	"github.com/fhs/gompd/v2/mpd"

	"jukem/internal/mpdctl"
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
		return Entry{Type: "directory", Path: d, Name: baseName(d)}, true
	}
	f := a["file"]
	if f == "" {
		return Entry{}, false
	}
	e := Entry{Type: "file", Path: f, Name: baseName(f), Title: a["Title"], Artist: a["Artist"], Album: a["Album"]}
	if d, err := strconv.ParseFloat(a["duration"], 64); err == nil {
		e.Duration = d
	} else if d, err := strconv.Atoi(a["Time"]); err == nil {
		e.Duration = float64(d)
	}
	if e.Title == "" {
		e.Title = strings.TrimSuffix(e.Name, extOf(e.Name))
	}
	return e, true
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func extOf(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[i:]
	}
	return ""
}

// Browse lists one folder: subfolders first, then tracks, both in
// case-insensitive name order, in pages of PageSize.
func (b *Browser) Browse(rel string, page int) (Page, error) {
	rel, err := CleanRel(rel)
	if err != nil {
		return Page{}, err
	}
	var entries []Entry
	err = b.pool.Do(func(c *mpd.Client) error {
		attrs, err := c.ListInfo(rel)
		if err != nil {
			return err
		}
		for _, a := range attrs {
			if e, ok := entryFrom(a); ok {
				entries = append(entries, e)
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
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	total := len(entries)
	pages := max(1, (total+PageSize-1)/PageSize)
	page = max(0, min(page, pages-1))
	start := page * PageSize
	end := min(start+PageSize, total)
	out := Page{Path: rel, Entries: entries[start:end], Total: total, Page: page, Pages: pages}
	if out.Entries == nil {
		out.Entries = []Entry{}
	}
	return out, nil
}

// SearchLimit caps search results.
const SearchLimit = 500

// Search finds tracks whose tags or file name contain q, case-insensitive.
func (b *Browser) Search(q string) ([]Entry, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []Entry{}, nil
	}
	seen := map[string]bool{}
	var out []Entry
	err := b.pool.Do(func(c *mpd.Client) error {
		for _, field := range []string{"any", "file"} {
			attrs, err := c.Search(field, q)
			if err != nil {
				return err
			}
			for _, a := range attrs {
				e, ok := entryFrom(a)
				if !ok || seen[e.Path] {
					continue
				}
				seen[e.Path] = true
				out = append(out, e)
				if len(out) >= SearchLimit {
					return nil
				}
			}
		}
		return nil
	})
	if out == nil {
		out = []Entry{}
	}
	return out, err
}

// Count returns the number of tracks MPD knows and the last database
// update as a unix time.
func (b *Browser) Count() (songs int, lastUpdate int64, err error) {
	err = b.pool.Do(func(c *mpd.Client) error {
		st, err := c.Stats()
		if err != nil {
			return err
		}
		songs, _ = strconv.Atoi(st["songs"])
		lastUpdate, _ = strconv.ParseInt(st["db_update"], 10, 64)
		return nil
	})
	return songs, lastUpdate, err
}
