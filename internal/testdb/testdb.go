// Package testdb gives each test its own migrated PostgreSQL database.
package testdb

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/db"
)

// EnvVar names a PostgreSQL URL for a role that can create databases.
const EnvVar = "INNKEEPER_TEST_DATABASE_URL"

// New creates a database, migrates it, and drops it when the test ends. It
// skips the test when EnvVar is unset, except in CI, where it fails.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(EnvVar)
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", EnvVar)
		}
		t.Skipf("%s is not set", EnvVar)
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("innkeeper_test_%x", rand.Uint64())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
		admin.Close(ctx)
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// The test image has pgvector, as the old migration assumed.
	if err := db.EnableVectors(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
