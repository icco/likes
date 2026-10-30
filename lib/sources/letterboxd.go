// Package sources fetches five-star titles from Letterboxd, Trakt, and AniList.
package sources

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.icco.me/likes/lib/likes"
)

// LetterboxdURL is the default Letterboxd base URL.
const LetterboxdURL = "https://letterboxd.com"

// userAgent identifies this service to the sites it reads.
const userAgent = "likes (+https://github.com/icco/likes)"

type letterboxdFeed struct {
	Items []struct {
		Link        string `xml:"link"`
		WatchedDate string `xml:"https://letterboxd.com watchedDate"`
		FilmTitle   string `xml:"https://letterboxd.com filmTitle"`
		FilmYear    string `xml:"https://letterboxd.com filmYear"`
		Rating      string `xml:"https://letterboxd.com memberRating"`
		TMDBID      string `xml:"https://themoviedb.org movieId"`
	} `xml:"channel>item"`
}

// Letterboxd reads five-star films from a member's RSS feed. Letterboxd blocks
// scraping the full ratings pages, and the feed only covers recent diary
// entries, so this source accumulates across runs.
func Letterboxd(hc *http.Client, baseURL, username string) likes.Source {
	return likes.Source{
		Name:       "letterboxd",
		Kind:       likes.Movies,
		Accumulate: true,
		Fetch: func(ctx context.Context) ([]likes.Item, error) {
			body, err := get(ctx, hc, fmt.Sprintf("%s/%s/rss/", baseURL, url.PathEscape(username)))
			if err != nil {
				return nil, err
			}
			var feed letterboxdFeed
			if err := xml.Unmarshal(body, &feed); err != nil {
				return nil, fmt.Errorf("parse letterboxd feed: %w", err)
			}
			var items []likes.Item
			for _, it := range feed.Items {
				if it.FilmTitle == "" {
					continue
				}
				if r, err := strconv.ParseFloat(it.Rating, 64); err != nil || r < 5 {
					continue
				}
				item := likes.Item{Title: it.FilmTitle, URL: it.Link, Source: "letterboxd"}
				item.Year, _ = strconv.Atoi(it.FilmYear)
				item.TMDBID, _ = strconv.Atoi(it.TMDBID)
				if d, err := time.Parse(time.DateOnly, it.WatchedDate); err == nil {
					item.Date = &d
				}
				items = append(items, item)
			}
			return items, nil
		},
	}
}

// get fetches rawURL and returns the body, treating any non-200 status as an
// error.
func get(ctx context.Context, hc *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", req.URL.Redacted(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", req.URL.Redacted(), err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: %s", req.URL.Redacted(), resp.Status)
	}
	return body, nil
}
