// Package dbtest gives each test its own Postgres schema, dropped on cleanup.
package dbtest

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DefaultDSN matches the Postgres service in .github/workflows/test.yml.
const DefaultDSN = "postgres://postgres:postgres@localhost:5432/likes_test?sslmode=disable" //nolint:gosec // test-only credential

// New connects to DATABASE_URL (or DefaultDSN), creates a schema named after
// the test, and scopes the connection to it.
func New(t testing.TB) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = DefaultDSN
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect to test Postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	// One connection keeps the session's search_path for every query.
	sqlDB.SetMaxOpenConns(1)

	schema := SchemaName(t.Name())
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	if err := db.Exec("SET search_path TO " + schema).Error; err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return db
}

// SchemaName turns a test name into a unique, valid Postgres identifier.
func SchemaName(testName string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, testName)
	if len(safe) > 40 {
		safe = safe[:40]
	}
	return fmt.Sprintf("t_%s_%d", safe, time.Now().UnixNano())
}
