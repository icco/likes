package likes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.icco.me/gutil/logging"
	"go.icco.me/gutil/render"
	"go.uber.org/zap"
)

// ErrBusy is returned by Refresh while another refresh is running.
var ErrBusy = errors.New("likes: refresh already running")

// state is what the state file holds: each source's last good fetch.
type state struct {
	Sources   map[string][]Item `json:"sources"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Result reports one source's outcome from a refresh.
type Result struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
	Error  string `json:"error,omitempty"`
}

// Service refreshes sources into a state file and serves the merged document.
type Service struct {
	path    string
	sources []Source
	running sync.Mutex

	mu      sync.RWMutex
	state   state
	doc     Document
	updated time.Time
}

// New loads the state file at path, if it exists, and returns a Service over
// sources. Source order sets which duplicate wins.
func New(path string, sources []Source) (*Service, error) {
	s := &Service{path: path, sources: sources, state: state{Sources: map[string][]Item{}}}
	b, err := os.ReadFile(path) //nolint:gosec // path is operator config, not user input
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read state: %w", err)
	default:
		if err := json.Unmarshal(b, &s.state); err != nil {
			return nil, fmt.Errorf("parse state %s: %w", path, err)
		}
		if s.state.Sources == nil {
			s.state.Sources = map[string][]Item{}
		}
	}
	s.doc = buildDocument(sources, s.state.Sources)
	s.updated = s.state.UpdatedAt
	return s, nil
}

// Document returns the current document and when it was last refreshed.
func (s *Service) Document() (Document, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.doc, s.updated
}

// Refresh fetches every source and saves the result. A source that fails
// keeps its previous items, and its error is joined into the returned error.
func (s *Service) Refresh(ctx context.Context) ([]Result, error) {
	if !s.running.TryLock() {
		return nil, ErrBusy
	}
	defer s.running.Unlock()

	s.mu.RLock()
	prev := s.state.Sources
	s.mu.RUnlock()

	next := state{Sources: make(map[string][]Item, len(s.sources)), UpdatedAt: time.Now().UTC()}
	results := make([]Result, 0, len(s.sources))
	var errs []error
	for _, src := range s.sources {
		items, err := src.Fetch(ctx)
		res := Result{Source: src.Name}
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", src.Name, err))
			res.Error = err.Error()
			items = prev[src.Name]
		case src.Accumulate:
			items = union(prev[src.Name], items)
		}
		res.Count = len(items)
		next.Sources[src.Name] = items
		results = append(results, res)
	}

	if err := s.save(next); err != nil {
		return results, errors.Join(append(errs, err)...)
	}
	return results, errors.Join(errs...)
}

// save writes st to the state file atomically, then swaps it in.
func (s *Service) save(st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.json")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}

	doc := buildDocument(s.sources, st.Sources)
	s.mu.Lock()
	s.state, s.doc, s.updated = st, doc, st.UpdatedAt
	s.mu.Unlock()
	return nil
}

// ServeLikes writes the document as JSON.
func (s *Service) ServeLikes(w http.ResponseWriter, r *http.Request) {
	doc, updated := s.Document()
	if !updated.IsZero() {
		w.Header().Set("Last-Modified", updated.Format(http.TimeFormat))
	}
	render.JSON(logging.FromContext(r.Context()), w, http.StatusOK, doc)
}

// ServeCron runs a refresh and reports each source's outcome. It answers 409
// while a refresh is already running and 502 when any source failed.
func (s *Service) ServeCron(w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(r.Context())
	results, err := s.Refresh(r.Context())
	status := http.StatusOK
	switch {
	case errors.Is(err, ErrBusy):
		status = http.StatusConflict
	case err != nil:
		log.Warnw("refresh failed", zap.Error(err))
		status = http.StatusBadGateway
	}
	render.JSON(log, w, status, map[string]any{"results": results})
}
