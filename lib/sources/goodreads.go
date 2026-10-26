package sources

import (
	"context"
	"fmt"

	"go.icco.me/goodreads"
	"go.icco.me/likes/lib/likes"
)

// Goodreads reads five-star books from a user's public "read" shelf. userID is
// the number in goodreads.com/user/show/<id>.
func Goodreads(c *goodreads.Client, userID string) likes.Source {
	return likes.Source{
		Name: "goodreads",
		Kind: likes.Books,
		Fetch: func(ctx context.Context) ([]likes.Item, error) {
			books, err := c.Shelf(ctx, userID, goodreads.ShelfRead)
			if err != nil {
				return nil, fmt.Errorf("goodreads shelf: %w", err)
			}
			var items []likes.Item
			for _, b := range books {
				if b.UserRating < 5 {
					continue
				}
				items = append(items, likes.Item{
					Title:  b.Title,
					Author: b.Author,
					Year:   b.Year,
					URL:    goodreads.BookURL(b.GoodreadsID),
					Source: "goodreads",
					Date:   b.ReadAt,
				})
			}
			return items, nil
		},
	}
}
