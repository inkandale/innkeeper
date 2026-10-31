package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoVectors means pgvector isn't available, so vector features are off.
var ErrNoVectors = errors.New("pgvector is not available; similar cards rank by tags and semantic search is off")

// EnableVectors creates the pgvector extension, if it can, and the table
// card vectors live in. It returns ErrNoVectors, wrapping the reason, only
// when pgvector isn't installed or the role may not create it; the caller
// logs that and carries on. Any other error is returned as is.
func EnableVectors(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Concurrent CREATE ... IF NOT EXISTS can fail with a unique violation, so
	// serialize with Migrate and with other starting processes.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-migrate'))`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		var pgErr *pgconn.PgError
		// 0A000: not installed (observed on postgres 16); 58P01: control file missing on some builds; 42501: not permitted.
		if errors.As(err, &pgErr) && (pgErr.Code == "0A000" || pgErr.Code == "58P01" || pgErr.Code == "42501") {
			return fmt.Errorf("%w: %v", ErrNoVectors, err)
		}
		return fmt.Errorf("create extension vector: %w", err)
	}
	// No fixed dimension: a vector's space says which model made it.
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS card_embedding (
		card_id bigint NOT NULL REFERENCES card (id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
		instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
		revision_id bigint NOT NULL,
		space text NOT NULL,
		embedding vector NOT NULL,
		PRIMARY KEY (instance_id, card_id),
		FOREIGN KEY (revision_id, card_id) REFERENCES card_revision (id, card_id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE
	)`); err != nil {
		return fmt.Errorf("create card_embedding: %w", err)
	}
	return tx.Commit(ctx)
}

// VectorsAvailable reports whether EnableVectors has succeeded on this
// database.
func VectorsAvailable(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx, `SELECT to_regclass('card_embedding') IS NOT NULL`).Scan(&ok)
	return ok, err
}
