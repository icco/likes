// Package main serves five-star books, movies, and TV shows as JSON,
// refreshed from Letterboxd, Trakt, and AniList by a cron hitting
// /cron.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"go.icco.me/anilist"
	"go.icco.me/gutil/etag"
	"go.icco.me/gutil/logging"
	"go.icco.me/gutil/tracing"
	"go.icco.me/likes/lib/likes"
	"go.icco.me/likes/lib/sources"
	"go.icco.me/trakt"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	gormtracing "gorm.io/plugin/opentelemetry/tracing"
)

const service = "likes"

func main() {
	log := logging.Must(logging.NewLogger(service))
	defer logging.Sync(log)
	ctx, stop := signal.NotifyContext(logging.NewContext(context.Background(), log), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := tracing.Init(ctx, service)
	if err != nil {
		log.Fatalw("initialize tracing", zap.Error(err))
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			log.Warnw("shutdown tracing", zap.Error(err))
		}
	}()

	db, err := openDB(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalw("open database", zap.Error(err))
	}
	svc, err := likes.New(ctx, db, configuredSources(os.Getenv, &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   30 * time.Second,
	}))
	if err != nil {
		log.Fatalw("start", zap.Error(err))
	}
	go func() {
		if _, err := svc.Refresh(ctx); err != nil {
			log.Warnw("startup refresh", zap.Error(err))
		}
	}()

	srv := &http.Server{
		Addr:              ":" + getenv("PORT", "8080"),
		Handler:           router(svc, log.Desugar()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Warnw("shutdown", zap.Error(err))
		}
	}()
	log.Infow("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalw("serve", zap.Error(err))
	}
}

// openDB connects to Postgres. Queries log nothing themselves; callers log the
// errors they return.
func openDB(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	if err := db.Use(gormtracing.NewPlugin(gormtracing.WithoutMetrics(), gormtracing.WithoutQueryVariables())); err != nil {
		return nil, fmt.Errorf("register tracing plugin: %w", err)
	}
	return db, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// configuredSources returns a source for each account set in the environment.
// Order sets which copy of a duplicate wins: Letterboxd, then Trakt, then
// AniList, which has no TMDb ids.
func configuredSources(env func(string) string, hc *http.Client) []likes.Source {
	var out []likes.Source
	if u := env("LETTERBOXD_USERNAME"); u != "" {
		out = append(out, sources.Letterboxd(hc, sources.LetterboxdURL, u))
	}
	if u, id := env("TRAKT_USERNAME"), env("TRAKT_CLIENT_ID"); u != "" && id != "" {
		tc := trakt.NewClient(id, "")
		out = append(out,
			sources.Trakt(tc, u, likes.Movies),
			sources.Trakt(tc, u, likes.TV),
			sources.TraktEpisodes(tc, u),
		)
	}
	if u := env("ANILIST_USERNAME"); u != "" {
		ac := anilist.NewClient()
		out = append(out, sources.AniList(ac, u, likes.Movies), sources.AniList(ac, u, likes.TV))
	}
	return out
}

func router(svc *likes.Service, log *zap.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(logging.Middleware(log))
	r.With(etag.Handler(false)).Get("/", svc.ServeLikes)
	r.Get("/cron", svc.ServeCron)
	return otelhttp.NewHandler(r, service,
		otelhttp.WithFilter(func(req *http.Request) bool {
			return req.URL.Path != "/metrics" && req.URL.Path != "/healthz"
		}),
	)
}
