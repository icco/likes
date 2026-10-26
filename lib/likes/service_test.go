package likes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stub is a source whose results the test controls.
type stub struct {
	items []Item
	err   error
}

func (s *stub) source(name string, kind Kind, accumulate bool) Source {
	return Source{Name: name, Kind: kind, Accumulate: accumulate, Fetch: func(context.Context) ([]Item, error) {
		return s.items, s.err
	}}
}

func TestNewMissingFile(t *testing.T) {
	svc, err := New(filepath.Join(t.TempDir(), "state.json"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doc, updated := svc.Document()
	if !updated.IsZero() || len(doc.Books) != 0 || doc.Books == nil {
		t.Errorf("doc = %+v, updated = %v, want empty", doc, updated)
	}
}

func TestNewBadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path, nil); err == nil {
		t.Error("New with corrupt state succeeded, want error")
	}
	if _, err := New(dir, nil); err == nil {
		t.Error("New with a directory as state succeeded, want error")
	}
}

func TestNewNullSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"sources":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(path, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if svc.state.Sources == nil {
		t.Error("Sources is nil after loading null")
	}
}

func TestRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	books := &stub{items: []Item{{Title: "A Wizard of Earthsea", Source: "goodreads"}}}
	feed := &stub{items: []Item{{Title: "Arrival", TMDBID: 1, Source: "letterboxd"}}}
	sources := []Source{
		books.source("goodreads", Books, false),
		feed.source("letterboxd", Movies, true),
	}

	svc, err := New(path, sources)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	// The feed rolls Arrival off, and Goodreads goes down.
	feed.items = []Item{{Title: "Heat", TMDBID: 2, Source: "letterboxd"}}
	books.items, books.err = nil, errors.New("goodreads is down")
	results, err := svc.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "goodreads is down") {
		t.Fatalf("Refresh error = %v, want the goodreads failure", err)
	}
	want := []Result{
		{Source: "goodreads", Count: 1, Error: "goodreads is down"},
		{Source: "letterboxd", Count: 2},
	}
	if len(results) != len(want) || results[0] != want[0] || results[1] != want[1] {
		t.Errorf("results = %+v, want %+v", results, want)
	}

	doc, updated := svc.Document()
	if updated.IsZero() {
		t.Error("updated is zero after a refresh")
	}
	if got := titles(doc.Books); !slices.Equal(got, []string{"A Wizard of Earthsea"}) {
		t.Errorf("books = %v, want the previous fetch kept", got)
	}
	if got := titles(doc.Movies); !slices.Equal(got, []string{"Arrival", "Heat"}) {
		t.Errorf("movies = %v, want the feed accumulated", got)
	}

	reloaded, err := New(path, sources)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, _ := reloaded.Document(); !slices.Equal(titles(got.Movies), titles(doc.Movies)) {
		t.Errorf("reloaded movies = %v, want %v", titles(got.Movies), titles(doc.Movies))
	}
}

func TestRefreshBusy(t *testing.T) {
	svc, err := New(filepath.Join(t.TempDir(), "state.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.running.Lock()
	defer svc.running.Unlock()
	if _, err := svc.Refresh(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("Refresh = %v, want ErrBusy", err)
	}
}

func TestRefreshSaveFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "state.json")
	svc, err := New(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(context.Background()); err == nil {
		t.Error("Refresh into a missing directory succeeded, want error")
	}
}

func TestRefreshRenameFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	svc, err := New(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A non-empty directory at the target path makes the rename fail.
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(context.Background()); err == nil {
		t.Error("Refresh onto a directory succeeded, want error")
	}
}

func TestServeLikes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	books := &stub{items: []Item{{Title: "A Wizard of Earthsea", Source: "goodreads"}}}
	svc, err := New(path, []Source{books.source("goodreads", Books, false)})
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	svc.ServeLikes(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if w.Header().Get("Last-Modified") != "" {
		t.Errorf("Last-Modified set before any refresh: %q", w.Header().Get("Last-Modified"))
	}

	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	svc.ServeLikes(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || w.Header().Get("Last-Modified") == "" {
		t.Errorf("status = %d, Last-Modified = %q", w.Code, w.Header().Get("Last-Modified"))
	}
	var doc map[string][]Item
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"books", "movies", "anime", "tv"} {
		if doc[k] == nil {
			t.Errorf("key %q missing or null in %s", k, w.Body.String())
		}
	}
	if len(doc) != 4 {
		t.Errorf("got keys %v, want exactly books, movies, anime, tv", doc)
	}
}

func TestServeCron(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	books := &stub{items: []Item{{Title: "A Wizard of Earthsea"}}}
	svc, err := New(path, []Source{books.source("goodreads", Books, false)})
	if err != nil {
		t.Fatal(err)
	}

	cron := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		svc.ServeCron(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cron", nil))
		return w
	}

	if w := cron(); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"count": 1`) {
		t.Errorf("ok: status = %d, body = %s", w.Code, w.Body.String())
	}

	books.err = errors.New("goodreads is down")
	if w := cron(); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "goodreads is down") {
		t.Errorf("failure: status = %d, body = %s", w.Code, w.Body.String())
	}

	svc.running.Lock()
	defer svc.running.Unlock()
	if w := cron(); w.Code != http.StatusConflict {
		t.Errorf("busy: status = %d, want 409", w.Code)
	}
}
