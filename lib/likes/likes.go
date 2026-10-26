// Package likes collects five-star ratings from several sites, persists them
// to a state file, and serves them as one JSON document.
package likes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Kind is the category a source's titles are listed under.
type Kind string

// Books are listed under "books".
const Books Kind = "books"

// Movies are listed under "movies".
const Movies Kind = "movies"

// Anime are listed under "anime".
const Anime Kind = "anime"

// TV shows are listed under "tv".
const TV Kind = "tv"

// Item is one five-star title.
type Item struct {
	Title  string     `json:"title"`
	Author string     `json:"author,omitempty"`
	Year   int        `json:"year,omitempty"`
	URL    string     `json:"url,omitempty"`
	Source string     `json:"source"`
	TMDBID int        `json:"tmdb_id,omitempty"`
	Date   *time.Time `json:"date,omitempty"`
}

// key identifies an item across sources: TMDb id when known, otherwise the
// title and year.
func (i Item) key() string {
	if i.TMDBID > 0 {
		return fmt.Sprintf("tmdb:%d", i.TMDBID)
	}
	return fmt.Sprintf("title:%s:%d", strings.ToLower(i.Title), i.Year)
}

// Source fetches one site's five-star titles.
type Source struct {
	// Name keys the source's items in the state file.
	Name string
	Kind Kind
	// Accumulate keeps titles from earlier runs, for feeds that only show
	// recent activity.
	Accumulate bool
	Fetch      func(context.Context) ([]Item, error)
}

// Document is the JSON the server returns.
type Document struct {
	Books  []Item `json:"books"`
	Movies []Item `json:"movies"`
	Anime  []Item `json:"anime"`
	TV     []Item `json:"tv"`
}

// buildDocument groups each source's items by kind, drops duplicates (the
// earlier source in sources wins), and sorts newest first.
func buildDocument(sources []Source, state map[string][]Item) Document {
	byKind := map[Kind][]Item{}
	seen := map[Kind]map[string]bool{}
	for _, src := range sources {
		if seen[src.Kind] == nil {
			seen[src.Kind] = map[string]bool{}
		}
		for _, it := range state[src.Name] {
			k := it.key()
			if seen[src.Kind][k] {
				continue
			}
			seen[src.Kind][k] = true
			byKind[src.Kind] = append(byKind[src.Kind], it)
		}
	}
	return Document{
		Books:  sorted(byKind[Books]),
		Movies: sorted(byKind[Movies]),
		Anime:  sorted(byKind[Anime]),
		TV:     sorted(byKind[TV]),
	}
}

// sorted orders items newest first, undated last, then by title. It never
// returns nil so empty kinds encode as [].
func sorted(items []Item) []Item {
	out := append([]Item{}, items...)
	sort.SliceStable(out, func(a, b int) bool {
		da, db := out[a].Date, out[b].Date
		switch {
		case da != nil && db != nil && !da.Equal(*db):
			return da.After(*db)
		case (da == nil) != (db == nil):
			return da != nil
		}
		return strings.ToLower(out[a].Title) < strings.ToLower(out[b].Title)
	})
	return out
}

// union returns fresh plus any old items fresh does not replace.
func union(old, fresh []Item) []Item {
	out := append([]Item{}, fresh...)
	have := make(map[string]bool, len(fresh))
	for _, it := range fresh {
		have[it.key()] = true
	}
	for _, it := range old {
		if !have[it.key()] {
			have[it.key()] = true
			out = append(out, it)
		}
	}
	return out
}
