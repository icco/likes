package sources

import (
	"context"
	"fmt"

	"go.icco.me/anilist"
	"go.icco.me/likes/lib/likes"
)

// AniList reads top-scored anime from a user's public list: films (format
// MOVIE) for likes.Movies, and every other format for likes.TV. The client
// normalizes every AniList score format to 0-10, so the top of the user's
// scale is 10.
func AniList(c *anilist.Client, username string, kind likes.Kind) likes.Source {
	movies := kind == likes.Movies
	name := "anilist-tv"
	if movies {
		name = "anilist-movies"
	}
	return likes.Source{
		Name: name,
		Kind: kind,
		Fetch: func(ctx context.Context) ([]likes.Item, error) {
			entries, err := c.List(ctx, username)
			if err != nil {
				return nil, fmt.Errorf("anilist list: %w", err)
			}
			var items []likes.Item
			for _, e := range entries {
				if e.Score < 10 || (e.Format == "MOVIE") != movies {
					continue
				}
				items = append(items, likes.Item{Title: e.Title, Year: e.Year, URL: e.URL, Source: "anilist"})
			}
			return items, nil
		},
	}
}
