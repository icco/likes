package likes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"go.icco.me/likes/lib/dbtest"
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

func newService(t *testing.T, sources ...Source) *Service {
	t.Helper()
	svc, err := New(t.Context(), dbtest.New(t), sources)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestDocumentEmpty(t *testing.T) {
	doc, updated, err := newService(t).Document(t.Context())
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if !updated.IsZero() || doc.Books == nil || len(doc.Books) != 0 {
		t.Errorf("doc = %+v, updated = %v, want empty", doc, updated)
	}
}

func TestRefresh(t *testing.T) {
	books := &stub{items: []Item{
		{Title: "A Wizard of Earthsea", Source: "goodreads"},
		{Title: "The Dispossessed", Source: "goodreads"},
	}}
	feed := &stub{items: []Item{
		{Title: "Arrival", TMDBID: 1, Source: "letterboxd", URL: "first"},
		{Title: "Arrival", TMDBID: 1, Source: "letterboxd", URL: "rewatch"},
	}}
	svc := newService(t,
		books.source("goodreads", Books, false),
		feed.source("letterboxd", Movies, true),
	)
	if _, err := svc.Refresh(t.Context()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	// Goodreads drops a book, the feed rolls Arrival off, then Goodreads
	// goes down.
	books.items = books.items[:1]
	feed.items = []Item{{Title: "Heat", TMDBID: 2, Source: "letterboxd"}}
	if _, err := svc.Refresh(t.Context()); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	books.items, books.err = nil, errors.New("goodreads is down")
	results, err := svc.Refresh(t.Context())
	if err == nil || !strings.Contains(err.Error(), "goodreads is down") {
		t.Fatalf("Refresh error = %v, want the goodreads failure", err)
	}
	want := []Result{
		{Source: "goodreads", Count: 1, Error: "goodreads is down"},
		{Source: "letterboxd", Count: 2},
	}
	if !slices.Equal(results, want) {
		t.Errorf("results = %+v, want %+v", results, want)
	}

	doc, updated, err := svc.Document(t.Context())
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if updated.IsZero() {
		t.Error("updated is zero after a refresh")
	}
	if got := titles(doc.Books); !slices.Equal(got, []string{"A Wizard of Earthsea"}) {
		t.Errorf("books = %v, want the dropped book gone and the rest kept through the outage", got)
	}
	if got := titles(doc.Movies); !slices.Equal(got, []string{"Arrival", "Heat"}) {
		t.Errorf("movies = %v, want the feed accumulated", got)
	}
	if doc.Movies[0].URL != "first" {
		t.Errorf("movies[0].URL = %q, want the first of the repeated keys", doc.Movies[0].URL)
	}
}

func TestRefreshEmptySource(t *testing.T) {
	books := &stub{items: []Item{{Title: "A Wizard of Earthsea"}}}
	svc := newService(t, books.source("goodreads", Books, false))
	if _, err := svc.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	books.items = nil
	results, err := svc.Refresh(t.Context())
	if err != nil || results[0].Count != 0 {
		t.Errorf("results = %+v, err = %v, want an emptied shelf to clear its rows", results, err)
	}
}

func TestRefreshBusy(t *testing.T) {
	svc := newService(t)
	svc.running.Lock()
	defer svc.running.Unlock()
	if _, err := svc.Refresh(t.Context()); !errors.Is(err, ErrBusy) {
		t.Errorf("Refresh = %v, want ErrBusy", err)
	}
}

func TestDatabaseErrors(t *testing.T) {
	books := &stub{items: []Item{{Title: "A Wizard of Earthsea"}}}
	svc := newService(t, books.source("goodreads", Books, false))

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := New(canceled, svc.db, nil); err == nil {
		t.Error("New with a canceled context succeeded, want error")
	}

	if err := svc.db.Migrator().DropTable(&record{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(t.Context()); err == nil {
		t.Error("Refresh without the items table succeeded, want error")
	}
	if _, _, err := svc.Document(t.Context()); err == nil {
		t.Error("Document without the items table succeeded, want error")
	}
	w := httptest.NewRecorder()
	svc.ServeLikes(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("ServeLikes status = %d, want 500", w.Code)
	}
}

func TestServeLikes(t *testing.T) {
	books := &stub{items: []Item{{Title: "A Wizard of Earthsea", Source: "goodreads"}}}
	svc := newService(t, books.source("goodreads", Books, false))

	w := httptest.NewRecorder()
	svc.ServeLikes(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if w.Header().Get("Last-Modified") != "" {
		t.Errorf("Last-Modified set before any refresh: %q", w.Header().Get("Last-Modified"))
	}

	if _, err := svc.Refresh(t.Context()); err != nil {
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
	books := &stub{items: []Item{{Title: "A Wizard of Earthsea"}}}
	svc := newService(t, books.source("goodreads", Books, false))

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
