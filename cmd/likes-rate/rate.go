package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type media struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
	} `json:"ids"`
}

type row struct {
	LastWatchedAt time.Time `json:"last_watched_at"`
	Movie         *media    `json:"movie"`
	Show          *media    `json:"show"`
}

type candidate struct {
	Kind    string
	Media   media
	Watched time.Time
}

type api struct {
	base, clientID, token string
	hc                    *http.Client
}

func (a api) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+"/"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", a.clientID)
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("trakt %s %s: HTTP %d: %s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (a api) rows(ctx context.Context, path string) ([]row, error) {
	var rows []row
	if err := a.do(ctx, http.MethodGet, path, nil, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (a api) rate(ctx context.Context, c candidate, rating int) error {
	entry := map[string]any{
		"rating":   rating,
		"rated_at": time.Now().UTC().Format(time.RFC3339),
		"ids":      map[string]int{"trakt": c.Media.IDs.Trakt},
	}
	return a.do(ctx, http.MethodPost, "sync/ratings", map[string]any{c.Kind: []any{entry}}, nil)
}

func candidates(ctx context.Context, a api, skipped map[int]bool) ([]candidate, error) {
	var out []candidate
	for _, kind := range []string{"movies", "shows"} {
		rated, err := a.rows(ctx, "sync/ratings/"+kind)
		if err != nil {
			return nil, err
		}
		done := map[int]bool{}
		for _, r := range rated {
			if m := pick(r); m != nil {
				done[m.IDs.Trakt] = true
			}
		}
		path := "sync/watched/" + kind
		if kind == "shows" {
			path += "?extended=noseasons"
		}
		watched, err := a.rows(ctx, path)
		if err != nil {
			return nil, err
		}
		for _, r := range watched {
			m := pick(r)
			if m == nil || done[m.IDs.Trakt] || skipped[skipKey(kind, m.IDs.Trakt)] {
				continue
			}
			out = append(out, candidate{Kind: kind, Media: *m, Watched: r.LastWatchedAt})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Watched.After(out[j].Watched) })
	return out, nil
}

func pick(r row) *media {
	if r.Movie != nil {
		return r.Movie
	}
	return r.Show
}

func skipKey(kind string, id int) int {
	if kind == "shows" {
		return -id
	}
	return id
}

func prompt(ctx context.Context, a api, cs []candidate, in io.Reader, out io.Writer, onSkip func(candidate) error) (rated int, err error) {
	sc := bufio.NewScanner(in)
	for i, c := range cs {
		label := "Movie"
		if c.Kind == "shows" {
			label = "Show"
		}
		for {
			fmt.Fprintf(out, "[%d/%d] %s: %s (%d), watched %s\n  https://trakt.tv/%s/%s\n  rating 1-10, s=skip, enter=later, q=quit > ",
				i+1, len(cs), label, c.Media.Title, c.Media.Year, c.Watched.Format(time.DateOnly), c.Kind, c.Media.IDs.Slug)
			if !sc.Scan() {
				return rated, sc.Err()
			}
			ans := strings.TrimSpace(strings.ToLower(sc.Text()))
			switch ans {
			case "q":
				return rated, nil
			case "":
			case "s":
				if err := onSkip(c); err != nil {
					return rated, err
				}
			default:
				n, convErr := strconv.Atoi(ans)
				if convErr != nil || n < 1 || n > 10 {
					fmt.Fprintln(out, "  enter 1-10, s, q, or nothing")
					continue
				}
				if err := a.rate(ctx, c, n); err != nil {
					return rated, err
				}
				rated++
			}
			break
		}
	}
	return rated, nil
}
