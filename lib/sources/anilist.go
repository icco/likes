package sources

import (
	"context"
	"fmt"

	"go.icco.me/anilist"
	"go.icco.me/likes/lib/likes"
)

// AniList reads top-scored anime from a user's public list. The client
// normalizes every AniList score format to 0-10, so the top of the user's
// scale is 10.
func AniList(c *anilist.Client, username string) likes.Source {
	return likes.Source{
		Name: "anilist",
		Kind: likes.Anime,
		Fetch: func(ctx context.Context) ([]likes.Item, error) {
			entries, err := c.List(ctx, username)
			if err != nil {
				return nil, fmt.Errorf("anilist list: %w", err)
			}
			var items []likes.Item
			for _, e := range entries {
				if e.Score < 10 {
					continue
				}
				items = append(items, likes.Item{Title: e.Title, Year: e.Year, URL: e.URL, Source: "anilist"})
			}
			return items, nil
		},
	}
}
