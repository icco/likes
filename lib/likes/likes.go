// Package likes collects five-star ratings from several sites, stores them in
// Postgres, and serves them as one JSON document.
package likes

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Kind is the category a source's titles are listed under.
type Kind string

// Books are listed under "books".
const Books Kind = "books"

// Movies are listed under "movies".
const Movies Kind = "movies"

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

// key is an item's primary identity: its TMDb id when known, otherwise its
// title and year.
func (i Item) key() string {
	return i.keys()[0]
}

// keys lists every identity an item matches on: its TMDb id when known, then
// its title and year. A source without TMDb ids, such as AniList, still
// matches another source's copy by title.
func (i Item) keys() []string {
	byTitle := fmt.Sprintf("title:%s:%d", normalizeTitle(i.Title), i.Year)
	if i.TMDBID > 0 {
		return []string{fmt.Sprintf("tmdb:%d", i.TMDBID), byTitle}
	}
	return []string{byTitle}
}

// normalizeTitle lowercases a title and keeps only letters and digits, so
// "Howl‘s Moving Castle" and "Howl's Moving Castle" match.
func normalizeTitle(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, title)
}

// Source fetches one site's five-star titles.
type Source struct {
	// Name keys the source's rows in the database.
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
	TV     []Item `json:"tv"`
}

// buildDocument groups each source's items by kind, drops an item when any of
// its keys was already seen (the earlier source in sources wins), and sorts
// newest first.
func buildDocument(sources []Source, state map[string][]Item) Document {
	byKind := map[Kind][]Item{}
	seen := map[Kind]map[string]bool{}
	for _, src := range sources {
		if seen[src.Kind] == nil {
			seen[src.Kind] = map[string]bool{}
		}
		for _, it := range state[src.Name] {
			keys := it.keys()
			if slices.ContainsFunc(keys, func(k string) bool { return seen[src.Kind][k] }) {
				continue
			}
			for _, k := range keys {
				seen[src.Kind][k] = true
			}
			byKind[src.Kind] = append(byKind[src.Kind], it)
		}
	}
	return Document{
		Books:  sorted(byKind[Books]),
		Movies: sorted(byKind[Movies]),
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
