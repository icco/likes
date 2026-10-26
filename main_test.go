package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"go.icco.me/likes/lib/dbtest"
	"go.icco.me/likes/lib/likes"
	"go.uber.org/zap"
)

func TestConfiguredSources(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		want []string
	}{
		"none": {env: map[string]string{}, want: nil},
		"all": {
			env: map[string]string{
				"LETTERBOXD_USERNAME": "alice",
				"TRAKT_USERNAME":      "alice",
				"TRAKT_CLIENT_ID":     "client-id",
				"GOODREADS_USER_ID":   "12345",
				"ANILIST_USERNAME":    "alice",
			},
			want: []string{"letterboxd", "trakt-movies", "trakt-shows", "trakt-episodes", "goodreads", "anilist-movies", "anilist-tv"},
		},
		"trakt without client id": {
			env:  map[string]string{"TRAKT_USERNAME": "alice"},
			want: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := configuredSources(func(k string) string { return tc.env[k] }, http.DefaultClient)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d sources, want %v", len(got), tc.want)
			}
			for i, src := range got {
				if src.Name != tc.want[i] {
					t.Errorf("source %d = %q, want %q", i, src.Name, tc.want[i])
				}
			}
		})
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("LIKES_TEST_SET", "value")
	if got := getenv("LIKES_TEST_SET", "fallback"); got != "value" {
		t.Errorf("getenv set = %q", got)
	}
	if got := getenv("LIKES_TEST_UNSET", "fallback"); got != "fallback" {
		t.Errorf("getenv unset = %q", got)
	}
}

func TestOpenDB(t *testing.T) {
	if _, err := openDB(""); err == nil {
		t.Error("openDB with no DSN succeeded, want error")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = dbtest.DefaultDSN
	}
	db, err := openDB(dsn)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Error(err)
	}
}

func TestRouter(t *testing.T) {
	svc, err := likes.New(t.Context(), dbtest.New(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	h := router(svc, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	etag := w.Header().Get("ETag")
	if w.Code != http.StatusOK || etag == "" {
		t.Fatalf("GET / = %d, ETag %q", w.Code, etag)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", etag)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotModified {
		t.Errorf("conditional GET / = %d, want 304", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cron", nil))
	if w.Code != http.StatusOK {
		t.Errorf("GET /cron = %d, body %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", w.Code)
	}
}
