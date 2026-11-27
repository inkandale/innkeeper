// Package db holds the core's PostgreSQL schema and applies it.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrNewerSchema means a newer Innkeeper has migrated the database, so this
// one would run against a schema it doesn't know.
var ErrNewerSchema = errors.New("the database was migrated by a newer Innkeeper; run that version or a later one")

// Migrate applies every migration not yet recorded in schema_migration, in
// file name order, in one transaction.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// The api and worker both migrate at startup and may start together.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-migrate'))`); err != nil {
		return err
	}
	// A migration that rewrites a big table may rightly take longer than a query may.
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = 0`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (
		name text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	// Only names after the newest file count: a renamed old migration can
	// leave a stale lower name behind.
	var newer string
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(name), '') FROM schema_migration WHERE name > $1`, path.Base(files[len(files)-1])).Scan(&newer); err != nil {
		return err
	}
	if newer != "" {
		return fmt.Errorf("%w (it has migration %s)", ErrNewerSchema, newer)
	}
	for _, f := range files {
		name := path.Base(f)
		var done bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migration WHERE name = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}
		// No arguments, so pgx uses the simple protocol, which allows several statements.
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migration (name) VALUES ($1)`, name); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
