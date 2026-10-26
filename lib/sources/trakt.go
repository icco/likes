package sources

import (
	"context"
	"fmt"
	"time"

	"go.icco.me/likes/lib/likes"
	"go.icco.me/trakt"
)

// A show counts as five-star from its episode ratings when at least
// minRatedEpisodes are rated and they average at least minEpisodeAverage.
const (
	minRatedEpisodes  = 3
	minEpisodeAverage = 9.5
)

// traktItem converts m to a likes item linked under trakt.tv/<typ>/.
func traktItem(m trakt.Media, typ string, rated time.Time) likes.Item {
	return likes.Item{
		Title:  m.Title,
		Year:   m.Year,
		URL:    fmt.Sprintf("https://trakt.tv/%s/%s", typ, m.IDs.Slug),
		Source: "trakt",
		TMDBID: m.IDs.TMDb,
		Date:   &rated,
	}
}

// Trakt reads a user's public 10/10 ratings of movies or shows. kind must be
// likes.Movies or likes.TV. c needs only a client ID.
func Trakt(c *trakt.Client, username string, kind likes.Kind) likes.Source {
	typ := "movies"
	if kind == likes.TV {
		typ = "shows"
	}
	return likes.Source{
		Name: "trakt-" + typ,
		Kind: kind,
		Fetch: func(ctx context.Context) ([]likes.Item, error) {
			rows, err := c.UserRatings(ctx, username, typ+"/10")
			if err != nil {
				return nil, fmt.Errorf("trakt %s ratings: %w", typ, err)
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
				items = append(items, traktItem(*m, typ, row.RatedAt))
			}
			return items, nil
		},
	}
}

// TraktEpisodes reads a user's public episode ratings and lists each show
// whose rated episodes clear minRatedEpisodes and minEpisodeAverage, dated by
// its newest episode rating. c needs only a client ID.
func TraktEpisodes(c *trakt.Client, username string) likes.Source {
	return likes.Source{
		Name: "trakt-episodes",
		Kind: likes.TV,
		Fetch: func(ctx context.Context) ([]likes.Item, error) {
			rows, err := c.UserRatings(ctx, username, "episodes")
			if err != nil {
				return nil, fmt.Errorf("trakt episode ratings: %w", err)
			}
			type tally struct {
				show   trakt.Media
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
				items = append(items, traktItem(t.show, "shows", t.newest))
			}
			return items, nil
		},
	}
}
