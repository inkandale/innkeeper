package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoVectors means pgvector isn't available, so vector features are off.
var ErrNoVectors = errors.New("pgvector is not available; similar cards rank by tags and semantic search is off")

// EnableVectors creates the pgvector extension, if it can, and the table
// card vectors live in. It returns ErrNoVectors, wrapping the reason, when
// the extension can't be created; the caller logs it and carries on.
func EnableVectors(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("%w: %v", ErrNoVectors, err)
	}
	// No fixed dimension: a vector's space says which model made it.
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS card_embedding (
		card_id bigint NOT NULL REFERENCES card (id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
		instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
		revision_id bigint NOT NULL,
		space text NOT NULL,
		embedding vector NOT NULL,
		PRIMARY KEY (instance_id, card_id),
		FOREIGN KEY (revision_id, card_id) REFERENCES card_revision (id, card_id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE
	)`)
	if err != nil {
		return fmt.Errorf("create card_embedding: %w", err)
	}
	return nil
}

// VectorsAvailable reports whether EnableVectors has succeeded on this
// database.
func VectorsAvailable(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx, `SELECT to_regclass('card_embedding') IS NOT NULL`).Scan(&ok)
	return ok, err
}
