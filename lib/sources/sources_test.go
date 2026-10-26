package sources

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"go.icco.me/anilist"
	"go.icco.me/goodreads"
	"go.icco.me/likes/lib/likes"
	"go.icco.me/trakt"
)

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

const letterboxdFeedXML = `<?xml version='1.0' encoding='utf-8'?>
<rss version="2.0" xmlns:letterboxd="https://letterboxd.com" xmlns:tmdb="https://themoviedb.org">
<channel>
  <item>
    <link>https://letterboxd.com/alice/film/arrival-2016/</link>
    <letterboxd:watchedDate>2026-10-11</letterboxd:watchedDate>
    <letterboxd:filmTitle>Arrival</letterboxd:filmTitle>
    <letterboxd:filmYear>2016</letterboxd:filmYear>
    <letterboxd:memberRating>5.0</letterboxd:memberRating>
    <tmdb:movieId>329865</tmdb:movieId>
  </item>
  <item>
    <link>https://letterboxd.com/alice/film/cats-2019/</link>
    <letterboxd:watchedDate>2026-09-27</letterboxd:watchedDate>
    <letterboxd:filmTitle>Cats</letterboxd:filmTitle>
    <letterboxd:filmYear>2019</letterboxd:filmYear>
    <letterboxd:memberRating>4.5</letterboxd:memberRating>
    <tmdb:movieId>536869</tmdb:movieId>
  </item>
  <item>
    <link>https://letterboxd.com/alice/film/unrated/</link>
    <letterboxd:filmTitle>Unrated</letterboxd:filmTitle>
  </item>
  <item>
    <link>https://letterboxd.com/alice/list/favourites/</link>
    <title>Favourites</title>
  </item>
</channel>
</rss>`

func TestLetterboxd(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alice/rss/" {
			http.NotFound(w, r)
			return
		}
		if ua := r.Header.Get("User-Agent"); ua != userAgent {
			t.Errorf("User-Agent = %q, want %q", ua, userAgent)
		}
		fmt.Fprint(w, letterboxdFeedXML)
	})

	src := Letterboxd(srv.Client(), srv.URL, "alice")
	if src.Name != "letterboxd" || src.Kind != likes.Movies || !src.Accumulate {
		t.Errorf("source = %+v, want accumulating letterboxd movies", src)
	}
	items, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want only the five-star film: %+v", len(items), items)
	}
	got := items[0]
	if got.Title != "Arrival" || got.Year != 2016 || got.TMDBID != 329865 || got.Source != "letterboxd" {
		t.Errorf("item = %+v", got)
	}
	if got.URL != "https://letterboxd.com/alice/film/arrival-2016/" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Date == nil || got.Date.Format("2006-01-02") != "2026-10-11" {
		t.Errorf("Date = %v, want 2026-10-11", got.Date)
	}
}

func TestLetterboxdErrors(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"status": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "blocked", http.StatusForbidden)
		},
		"bad xml": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "<rss><channel><item>")
		},
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			srv := serve(t, h)
			if _, err := Letterboxd(srv.Client(), srv.URL, "alice").Fetch(context.Background()); err == nil {
				t.Error("Fetch succeeded, want error")
			}
		})
	}
}

const traktMoviesJSON = `[
  {"rated_at":"2018-09-02T16:15:20.000Z","rating":10,"type":"movie","movie":{"title":"Arrival","year":2016,"ids":{"trakt":1,"slug":"arrival-2016","tmdb":329865}}},
  {"rated_at":"2014-01-01T00:00:00.000Z","rating":10,"type":"movie"}
]`

const traktShowsJSON = `[
  {"rated_at":"2014-06-21T18:35:36.000Z","rating":10,"type":"show","show":{"title":"Adventure Time","year":2010,"ids":{"trakt":15195,"slug":"adventure-time","tmdb":15260}}}
]`

// traktEpisode renders one episode rating for the show with the given trakt id.
func traktEpisode(showID int, title string, rating int, ratedAt string) string {
	return fmt.Sprintf(`{"rated_at":%q,"rating":%d,"type":"episode","show":{"title":%q,"year":2014,"ids":{"trakt":%d,"slug":"show-%d","tmdb":%d}}}`,
		ratedAt, rating, title, showID, showID, showID*10)
}

// Broad City averages 9.67 over 3 and Archer exactly 9.5 over 4, so both
// count. Girls has too few rated episodes and Gossip Girl averages 9.33.
var traktEpisodesJSON = "[" + strings.Join([]string{
	traktEpisode(1, "Broad City", 10, "2017-01-01T00:00:00Z"),
	traktEpisode(1, "Broad City", 10, "2017-12-10T00:00:00Z"),
	traktEpisode(1, "Broad City", 9, "2017-06-01T00:00:00Z"),
	traktEpisode(2, "Archer", 10, "2018-01-01T00:00:00Z"),
	traktEpisode(2, "Archer", 10, "2018-01-02T00:00:00Z"),
	traktEpisode(2, "Archer", 10, "2018-01-03T00:00:00Z"),
	traktEpisode(2, "Archer", 8, "2018-01-04T00:00:00Z"),
	traktEpisode(3, "Girls", 10, "2017-04-19T00:00:00Z"),
	traktEpisode(3, "Girls", 10, "2017-04-20T00:00:00Z"),
	traktEpisode(4, "Gossip Girl", 10, "2018-12-01T00:00:00Z"),
	traktEpisode(4, "Gossip Girl", 9, "2018-12-02T00:00:00Z"),
	traktEpisode(4, "Gossip Girl", 9, "2018-12-03T00:00:00Z"),
	`{"rated_at":"2018-12-04T00:00:00Z","rating":10,"type":"episode"}`,
}, ",") + "]"

// traktClient returns a trakt client with clientID pointed at srv.
func traktClient(srv *httptest.Server, clientID string) *trakt.Client {
	c := trakt.NewClient(clientID, "")
	c.BaseURL = srv.URL
	return c
}

func traktServer(t *testing.T) *httptest.Server {
	t.Helper()
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("trakt-api-key") != "client-id" || r.Header.Get("trakt-api-version") != "2" {
			http.Error(w, "missing api headers", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/users/alice/ratings/movies/10":
			fmt.Fprint(w, traktMoviesJSON)
		case "/users/alice/ratings/shows/10":
			fmt.Fprint(w, traktShowsJSON)
		case "/users/alice/ratings/episodes":
			fmt.Fprint(w, traktEpisodesJSON)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestTraktMovies(t *testing.T) {
	srv := traktServer(t)
	src := Trakt(traktClient(srv, "client-id"), "alice", likes.Movies)
	if src.Name != "trakt-movies" || src.Kind != likes.Movies || src.Accumulate {
		t.Errorf("source = %+v", src)
	}
	items, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (rows without media dropped): %+v", len(items), items)
	}
	got := items[0]
	if got.Title != "Arrival" || got.Year != 2016 || got.TMDBID != 329865 || got.URL != "https://trakt.tv/movies/arrival-2016" {
		t.Errorf("item = %+v", got)
	}
	if got.Date == nil || got.Date.Year() != 2018 {
		t.Errorf("Date = %v, want 2018", got.Date)
	}
}

func TestTraktShows(t *testing.T) {
	srv := traktServer(t)
	src := Trakt(traktClient(srv, "client-id"), "alice", likes.TV)
	if src.Name != "trakt-shows" || src.Kind != likes.TV {
		t.Errorf("source = %+v", src)
	}
	items, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Adventure Time" || items[0].URL != "https://trakt.tv/shows/adventure-time" {
		t.Errorf("items = %+v", items)
	}
}

func TestTraktEpisodes(t *testing.T) {
	srv := traktServer(t)
	src := TraktEpisodes(traktClient(srv, "client-id"), "alice")
	if src.Name != "trakt-episodes" || src.Kind != likes.TV || src.Accumulate {
		t.Errorf("source = %+v", src)
	}
	items, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Title)
	}
	if want := []string{"Broad City", "Archer"}; !slices.Equal(got, want) {
		t.Fatalf("shows = %v, want %v", got, want)
	}
	bc := items[0]
	if bc.URL != "https://trakt.tv/shows/show-1" || bc.TMDBID != 10 || bc.Source != "trakt" || bc.Year != 2014 {
		t.Errorf("item = %+v", bc)
	}
	if bc.Date == nil || bc.Date.Format("2006-01-02") != "2017-12-10" {
		t.Errorf("Date = %v, want the newest episode rating, 2017-12-10", bc.Date)
	}
}

func TestTraktErrors(t *testing.T) {
	srv := traktServer(t)
	if _, err := Trakt(traktClient(srv, "wrong-id"), "alice", likes.Movies).Fetch(context.Background()); err == nil {
		t.Error("Fetch with a bad client id succeeded, want error")
	}
	if _, err := TraktEpisodes(traktClient(srv, "wrong-id"), "alice").Fetch(context.Background()); err == nil {
		t.Error("episodes Fetch with a bad client id succeeded, want error")
	}

	bad := serve(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "{") })
	if _, err := Trakt(traktClient(bad, "client-id"), "alice", likes.Movies).Fetch(context.Background()); err == nil {
		t.Error("Fetch of bad json succeeded, want error")
	}
}

func goodreadsFeed(items ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel>` + strings.Join(items, "") + `</channel></rss>`
}

func goodreadsItem(id, title string, rating int) string {
	return fmt.Sprintf(`<item>
  <book_id>%s</book_id>
  <title><![CDATA[%s]]></title>
  <author_name>Ursula K. Le Guin</author_name>
  <user_rating>%d</user_rating>
  <book_published>1968</book_published>
  <user_read_at><![CDATA[Mon, 27 Jul 2026 00:00:00 +0000]]></user_read_at>
</item>`, id, title, rating)
}

func TestGoodreads(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/review/list_rss/12345" || r.URL.Query().Get("shelf") != goodreads.ShelfRead {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") != "1" {
			fmt.Fprint(w, goodreadsFeed())
			return
		}
		fmt.Fprint(w, goodreadsFeed(
			goodreadsItem("807968", "A Wizard of Earthsea", 5),
			goodreadsItem("13651", "The Dispossessed", 4),
			goodreadsItem("18423", "The Left Hand of Darkness", 0),
		))
	})

	c := goodreads.NewClient()
	c.URL = srv.URL
	c.SetHTTPClient(srv.Client())
	src := Goodreads(c, "12345")
	if src.Name != "goodreads" || src.Kind != likes.Books {
		t.Errorf("source = %+v", src)
	}
	items, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want only the five-star book: %+v", len(items), items)
	}
	got := items[0]
	if got.Title != "A Wizard of Earthsea" || got.Author != "Ursula K. Le Guin" || got.Year != 1968 {
		t.Errorf("item = %+v", got)
	}
	if got.URL != "https://www.goodreads.com/book/show/807968" || got.Date == nil {
		t.Errorf("URL = %q, Date = %v", got.URL, got.Date)
	}
}

func TestGoodreadsError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	c := goodreads.NewClient()
	c.URL = srv.URL
	if _, err := Goodreads(c, "12345").Fetch(context.Background()); err == nil {
		t.Error("Fetch succeeded, want error")
	}
}

const anilistJSON = `{"data":{
  "User":{"mediaListOptions":{"scoreFormat":"POINT_3"}},
  "MediaListCollection":{"lists":[{"entries":[
    {"score":3,"media":{"id":21087,"siteUrl":"https://anilist.co/anime/21087","format":"TV","seasonYear":2015,"title":{"english":"One Punch Man","romaji":"One Punch Man"}}},
    {"score":3,"media":{"id":110,"siteUrl":"https://anilist.co/anime/110","format":"MOVIE","seasonYear":2012,"title":{"english":"Wolf Children","romaji":"Ookami Kodomo no Ame to Yuki"}}},
    {"score":3,"media":{"id":44,"siteUrl":"https://anilist.co/anime/44","format":"OVA","seasonYear":1999,"title":{"english":"Trust & Betrayal","romaji":"Tsuioku-hen"}}},
    {"score":2,"media":{"format":"MOVIE","seasonYear":2019,"title":{"english":"","romaji":"Dororo"}}}
  ]}]}
}}`

func TestAniList(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		fmt.Fprint(w, anilistJSON)
	})

	c := anilist.NewClient()
	c.URL = srv.URL
	tests := []struct {
		kind likes.Kind
		name string
		want []string
	}{
		{likes.Movies, "anilist-movies", []string{"Wolf Children https://anilist.co/anime/110"}},
		{likes.TV, "anilist-tv", []string{"One Punch Man https://anilist.co/anime/21087", "Trust & Betrayal https://anilist.co/anime/44"}},
	}
	for _, tc := range tests {
		src := AniList(c, "alice", tc.kind)
		if src.Name != tc.name || src.Kind != tc.kind {
			t.Errorf("source = %+v, want %s", src, tc.name)
		}
		items, err := src.Fetch(context.Background())
		if err != nil {
			t.Fatalf("%s Fetch: %v", tc.name, err)
		}
		var got []string
		for _, it := range items {
			got = append(got, it.Title+" "+it.URL)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s items = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAniListError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	c := anilist.NewClient()
	c.URL = srv.URL
	if _, err := AniList(c, "alice", likes.Movies).Fetch(context.Background()); err == nil {
		t.Error("Fetch succeeded, want error")
	}
}

func TestGetBadURL(t *testing.T) {
	if _, err := get(context.Background(), http.DefaultClient, "://bad"); err == nil {
		t.Error("get with a bad URL succeeded, want error")
	}
	if _, err := get(context.Background(), http.DefaultClient, "http://127.0.0.1:0/"); err == nil {
		t.Error("get with an unreachable host succeeded, want error")
	}
}
