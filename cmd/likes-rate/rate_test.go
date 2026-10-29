package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCandidatesAndPrompt(t *testing.T) {
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("trakt-api-key") != "cid" {
			t.Errorf("missing auth headers on %s", r.URL)
		}
		switch r.URL.Path {
		case "/sync/ratings/movies":
			if _, err := io.WriteString(w, `[{"movie":{"title":"Rated","ids":{"trakt":1}}}]`); err != nil {
				t.Fatal(err)
			}
		case "/sync/ratings/shows":
			if _, err := io.WriteString(w, `[]`); err != nil {
				t.Fatal(err)
			}
		case "/sync/watched/movies":
			if _, err := io.WriteString(w, `[
			{"last_watched_at":"2020-01-01T00:00:00Z","movie":{"title":"Rated","year":2000,"ids":{"trakt":1}}},
			{"last_watched_at":"2021-01-01T00:00:00Z","movie":{"title":"Old","year":2001,"ids":{"trakt":2}}},
			{"last_watched_at":"2023-01-01T00:00:00Z","movie":{"title":"Skipped","year":2002,"ids":{"trakt":3}}}]`); err != nil {
				t.Fatal(err)
			}
		case "/sync/watched/shows":
			if _, err := io.WriteString(w, `[{"last_watched_at":"2022-01-01T00:00:00Z","show":{"title":"Show","year":2010,"ids":{"trakt":2}}}]`); err != nil {
				t.Fatal(err)
			}
		case "/sync/ratings":
			b, _ := io.ReadAll(r.Body)
			posted = append(posted, string(b))
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()

	a := api{base: srv.URL, clientID: "cid", token: "tok", hc: srv.Client()}
	ctx := context.Background()
	cs, err := candidates(ctx, a, map[int]bool{3: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Media.Title != "Show" || cs[1].Media.Title != "Old" {
		t.Fatalf("candidates = %+v", cs)
	}

	var skips []int
	n, err := prompt(ctx, a, cs, strings.NewReader("11\n10\ns\n"), io.Discard, func(c candidate) error {
		skips = append(skips, skipKey(c.Kind, c.Media.IDs.Trakt))
		return nil
	})
	if err != nil || n != 1 {
		t.Fatalf("prompt = %d, %v", n, err)
	}
	if len(skips) != 1 || skips[0] != 2 {
		t.Errorf("skips = %v", skips)
	}
	if len(posted) != 1 {
		t.Fatalf("posted = %v", posted)
	}
	var body map[string][]struct {
		Rating int            `json:"rating"`
		IDs    map[string]int `json:"ids"`
	}
	if err := json.Unmarshal([]byte(posted[0]), &body); err != nil {
		t.Fatal(err)
	}
	if s := body["shows"]; len(s) != 1 || s[0].Rating != 10 || s[0].IDs["trakt"] != 2 {
		t.Errorf("body = %s", posted[0])
	}
}
