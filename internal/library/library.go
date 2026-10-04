// Package library stores cards and lorebooks as append-only revisions, along
// with blobs, tags, collections, and duplicate lookups.
package library

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/cardhash"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrConflict means the write's base revision is no longer the head.
	ErrConflict  = errors.New("base revision is not the head")
	ErrSlugTaken = errors.New("slug already in use")
)

// Library is the core's card and lorebook store.
type Library struct {
	Cards     *Entities
	Lorebooks *Entities

	pool  *pgxpool.Pool
	blobs *blobstore.Store
}

// New returns a Library that writes new blobs to blobs.
func New(pool *pgxpool.Pool, blobs *blobstore.Store) *Library {
	return &Library{
		Cards: &Entities{pool: pool, k: kind{
			table: "card", revTable: "card_revision", fk: "card_id",
			simhash: cardhash.CardSimhash,
			forbid:  []string{"character_book"},
		}},
		Lorebooks: &Entities{pool: pool, k: kind{
			table: "lorebook", revTable: "lorebook_revision", fk: "lorebook_id",
			simhash: cardhash.LorebookSimhash,
		}},
		pool:  pool,
		blobs: blobs,
	}
}

// mapErr turns a missing row or a foreign key violation into ErrNotFound.
func mapErr(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23503":
		return errors.Join(ErrNotFound, err)
	}
	return err
}
