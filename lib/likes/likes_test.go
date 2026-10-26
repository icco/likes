package likes

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func date(s string) *time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return &d
}

func titles(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

func TestBuildDocument(t *testing.T) {
	sources := []Source{
		{Name: "letterboxd", Kind: Movies},
		{Name: "trakt-movies", Kind: Movies},
		{Name: "trakt-shows", Kind: TV},
		{Name: "goodreads", Kind: Books},
	}
	state := map[string][]Item{
		"letterboxd": {
			{Title: "Arrival", TMDBID: 329865, Source: "letterboxd", Date: date("2026-10-11")},
			{Title: "Arrival", TMDBID: 329865, Source: "letterboxd", Date: date("2025-01-01")},
		},
		"trakt-movies": {
			{Title: "Arrival", TMDBID: 329865, Source: "trakt", Date: date("2018-09-02")},
			{Title: "Heat", TMDBID: 949, Source: "trakt", Date: date("2018-01-01")},
			{Title: "Brick", Year: 2005, Source: "trakt"},
			{Title: "akira", Year: 1988, Source: "trakt"},
		},
		"goodreads": {
			{Title: "A Wizard of Earthsea", Source: "goodreads", Date: date("2026-07-27")},
		},
		"removed-source": {{Title: "Ignored"}},
	}

	doc := buildDocument(sources, state)

	if got, want := titles(doc.Movies), []string{"Arrival", "Heat", "akira", "Brick"}; !slices.Equal(got, want) {
		t.Errorf("movies = %v, want %v", got, want)
	}
	if doc.Movies[0].Source != "letterboxd" || !doc.Movies[0].Date.Equal(*date("2026-10-11")) {
		t.Errorf("movies[0] = %+v, want the newest letterboxd entry", doc.Movies[0])
	}
	if got := titles(doc.Books); !slices.Equal(got, []string{"A Wizard of Earthsea"}) {
		t.Errorf("books = %v", got)
	}
	if doc.TV == nil || len(doc.TV) != 0 {
		t.Errorf("empty kinds must be non-nil and empty: tv=%#v", doc.TV)
	}
}

func TestBuildDocumentMatchesTitleWithoutTMDB(t *testing.T) {
	sources := []Source{
		{Name: "trakt-movies", Kind: Movies},
		{Name: "anilist-movies", Kind: Movies},
	}
	state := map[string][]Item{
		"trakt-movies": {
			{Title: "Howl's Moving Castle", Year: 2004, TMDBID: 4935, Source: "trakt"},
		},
		"anilist-movies": {
			{Title: "Howl‘s Moving Castle", Year: 2004, Source: "anilist"},
			{Title: "Howl's Moving Castle", Year: 2005, Source: "anilist"},
			{Title: "Wolf Children", Year: 2012, Source: "anilist"},
		},
	}

	doc := buildDocument(sources, state)

	var got []string
	for _, it := range doc.Movies {
		got = append(got, fmt.Sprintf("%s/%d/%s", it.Title, it.Year, it.Source))
	}
	want := []string{"Howl's Moving Castle/2004/trakt", "Howl's Moving Castle/2005/anilist", "Wolf Children/2012/anilist"}
	if !slices.Equal(got, want) {
		t.Errorf("movies = %v, want %v", got, want)
	}
}

func TestNormalizeTitle(t *testing.T) {
	if got := normalizeTitle("HAIKYU!! The Dumpster Battle"); got != "haikyuthedumpsterbattle" {
		t.Errorf("normalizeTitle = %q", got)
	}
}

func TestSortedTiesOnDate(t *testing.T) {
	d := date("2020-01-01")
	got := titles(sorted([]Item{{Title: "b", Date: d}, {Title: "A", Date: d}, {Title: "c", Date: date("2021-01-01")}}))
	if want := []string{"c", "A", "b"}; !slices.Equal(got, want) {
		t.Errorf("sorted = %v, want %v", got, want)
	}
}
