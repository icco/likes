package likes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.icco.me/gutil/logging"
	"go.icco.me/gutil/render"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrBusy is returned by Refresh while another refresh is running.
var ErrBusy = errors.New("likes: refresh already running")

// record is an Item stored under the source that fetched it.
type record struct {
	Feed      string `gorm:"primaryKey"`
	ItemKey   string `gorm:"primaryKey"`
	Item      `gorm:"embedded"`
	UpdatedAt time.Time
}

// TableName keeps the table name stable if record is renamed.
func (record) TableName() string { return "items" }

// Result reports one source's outcome from a refresh.
type Result struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
	Error  string `json:"error,omitempty"`
}

// Service refreshes sources into Postgres and serves the merged document.
type Service struct {
	db      *gorm.DB
	sources []Source
	running sync.Mutex
}

// New migrates the schema and returns a Service over sources. Source order
// sets which duplicate wins.
func New(ctx context.Context, db *gorm.DB, sources []Source) (*Service, error) {
	if err := db.WithContext(ctx).AutoMigrate(&record{}); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Service{db: db, sources: sources}, nil
}

// Document returns the current document.
func (s *Service) Document(ctx context.Context) (Document, error) {
	var rows []record
	if err := s.db.WithContext(ctx).Order("feed, item_key").Find(&rows).Error; err != nil {
		return Document{}, fmt.Errorf("load items: %w", err)
	}
	byFeed := map[string][]Item{}
	for _, r := range rows {
		byFeed[r.Feed] = append(byFeed[r.Feed], r.Item)
	}
	return buildDocument(s.sources, byFeed), nil
}

// Refresh fetches every source and saves the result. A source that fails
// keeps its previous items, and its error is joined into the returned error.
func (s *Service) Refresh(ctx context.Context) ([]Result, error) {
	if !s.running.TryLock() {
		return nil, ErrBusy
	}
	defer s.running.Unlock()

	results := make([]Result, 0, len(s.sources))
	var errs []error
	for _, src := range s.sources {
		res := Result{Source: src.Name}
		count, err := s.refreshSource(ctx, src)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.Name, err))
			res.Error = err.Error()
		}
		res.Count = count
		results = append(results, res)
	}
	return results, errors.Join(errs...)
}

// refreshSource fetches one source and writes it in a single transaction:
// replacing its rows, or adding to them for an accumulating source. It
// returns how many rows the source has afterwards.
func (s *Service) refreshSource(ctx context.Context, src Source) (int, error) {
	items, fetchErr := src.Fetch(ctx)
	var count int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if fetchErr == nil {
			if !src.Accumulate {
				if err := tx.Where("feed = ?", src.Name).Delete(&record{}).Error; err != nil {
					return fmt.Errorf("clear: %w", err)
				}
			}
			if rows := records(src.Name, items); len(rows) > 0 {
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(rows, 100).Error; err != nil {
					return fmt.Errorf("save: %w", err)
				}
			}
		}
		return tx.Model(&record{}).Where("feed = ?", src.Name).Count(&count).Error
	})
	return int(count), errors.Join(fetchErr, err)
}

// records converts items to rows, dropping repeats of a key so one insert
// never touches the same row twice.
func records(feed string, items []Item) []record {
	seen := map[string]bool{}
	var out []record
	for _, it := range items {
		k := it.key()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, record{Feed: feed, ItemKey: k, Item: it})
	}
	return out
}

// ServeLikes writes the document as JSON.
func (s *Service) ServeLikes(w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(r.Context())
	doc, err := s.Document(r.Context())
	if err != nil {
		log.Errorw("load document", zap.Error(err))
		render.JSON(log, w, http.StatusInternalServerError, map[string]string{"error": "could not load likes"})
		return
	}
	render.JSON(log, w, http.StatusOK, doc)
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
