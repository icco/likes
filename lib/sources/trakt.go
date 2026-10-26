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

type traktRating struct {
	RatedAt time.Time   `json:"rated_at"`
	Movie   *traktMedia `json:"movie"`
	Show    *traktMedia `json:"show"`
}

type traktMedia struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   struct {
		Slug string `json:"slug"`
		TMDb int    `json:"tmdb"`
	} `json:"ids"`
}

// Trakt reads a user's public 10/10 ratings of movies or shows. Public
// profiles need only an API app's client ID, not OAuth. kind must be
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
			header := http.Header{}
			header.Set("Content-Type", "application/json")
			header.Set("trakt-api-key", clientID)
			header.Set("trakt-api-version", "2")
			body, err := get(ctx, hc, fmt.Sprintf("%s/users/%s/ratings/%s/10", baseURL, url.PathEscape(username), typ), header)
			if err != nil {
				return nil, err
			}
			var rows []traktRating
			if err := json.Unmarshal(body, &rows); err != nil {
				return nil, fmt.Errorf("parse trakt ratings: %w", err)
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
				rated := row.RatedAt
				items = append(items, likes.Item{
					Title:  m.Title,
					Year:   m.Year,
					URL:    fmt.Sprintf("https://trakt.tv/%s/%s", typ, m.IDs.Slug),
					Source: "trakt",
					TMDBID: m.IDs.TMDb,
					Date:   &rated,
				})
			}
			return items, nil
		},
	}
}
