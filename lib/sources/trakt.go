package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.icco.me/likes/lib/likes"
)

// TraktURL is the default Trakt API base URL.
const TraktURL = "https://api.trakt.tv"

// A show counts as five-star from its episode ratings when at least
// minRatedEpisodes are rated and they average at least minEpisodeAverage.
const (
	minRatedEpisodes  = 3
	minEpisodeAverage = 9.5
)

type traktRating struct {
	RatedAt time.Time   `json:"rated_at"`
	Rating  int         `json:"rating"`
	Movie   *traktMedia `json:"movie"`
	Show    *traktMedia `json:"show"`
}

type traktMedia struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
		TMDb  int    `json:"tmdb"`
	} `json:"ids"`
}

// item converts m to a likes item linked under trakt.tv/<typ>/.
func (m traktMedia) item(typ string, rated time.Time) likes.Item {
	return likes.Item{
		Title:  m.Title,
		Year:   m.Year,
		URL:    fmt.Sprintf("https://trakt.tv/%s/%s", typ, m.IDs.Slug),
		Source: "trakt",
		TMDBID: m.IDs.TMDb,
		Date:   &rated,
	}
}

// traktRatings fetches users/<username>/ratings/<path>. Public profiles need
// only an API app's client ID, not OAuth.
func traktRatings(ctx context.Context, hc *http.Client, baseURL, clientID, username, path string) ([]traktRating, error) {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("trakt-api-key", clientID)
	header.Set("trakt-api-version", "2")
	body, err := get(ctx, hc, fmt.Sprintf("%s/users/%s/ratings/%s", baseURL, url.PathEscape(username), path), header)
	if err != nil {
		return nil, err
	}
	var rows []traktRating
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("parse trakt ratings: %w", err)
	}
	return rows, nil
}

// Trakt reads a user's public 10/10 ratings of movies or shows. kind must be
// likes.Movies or likes.TV.
func Trakt(hc *http.Client, baseURL, clientID, username string, kind likes.Kind) likes.Source {
	typ := "movies"
	if kind == likes.TV {
		typ = "shows"
	}
	return likes.Source{
		Name: "trakt-" + typ,
		Kind: kind,
		Fetch: func(ctx context.Context) ([]likes.Item, error) {
			rows, err := traktRatings(ctx, hc, baseURL, clientID, username, typ+"/10")
			if err != nil {
				return nil, err
			}
			items := make([]likes.Item, 0, len(rows))
			for _, row := range rows {
				m := row.Movie
				if m == nil {
					m = row.Show
				}
				if m == nil {
					continue
				}
				items = append(items, m.item(typ, row.RatedAt))
			}
			return items, nil
		},
	}
}

// TraktEpisodes reads a user's public episode ratings and lists each show
// whose rated episodes clear minRatedEpisodes and minEpisodeAverage, dated by
// its newest episode rating.
func TraktEpisodes(hc *http.Client, baseURL, clientID, username string) likes.Source {
	return likes.Source{
		Name: "trakt-episodes",
		Kind: likes.TV,
		Fetch: func(ctx context.Context) ([]likes.Item, error) {
			rows, err := traktRatings(ctx, hc, baseURL, clientID, username, "episodes")
			if err != nil {
				return nil, err
			}
			type tally struct {
				show   traktMedia
				count  int
				sum    int
				newest time.Time
			}
			var order []int
			shows := map[int]*tally{}
			for _, row := range rows {
				if row.Show == nil {
					continue
				}
				id := row.Show.IDs.Trakt
				t, ok := shows[id]
				if !ok {
					t = &tally{show: *row.Show}
					shows[id] = t
					order = append(order, id)
				}
				t.count++
				t.sum += row.Rating
				if row.RatedAt.After(t.newest) {
					t.newest = row.RatedAt
				}
			}
			var items []likes.Item
			for _, id := range order {
				t := shows[id]
				if t.count < minRatedEpisodes || float64(t.sum)/float64(t.count) < minEpisodeAverage {
					continue
				}
				items = append(items, t.show.item("shows", t.newest))
			}
			return items, nil
		},
	}
}
