// Package library stores cards and lorebooks as append-only revisions, along
// with blobs, tags, collections, and duplicate lookups.
package library

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/db"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrConflict means the write's base revision is no longer the head.
	ErrConflict  = errors.New("base revision is not the head")
	ErrSlugTaken = errors.New("slug already in use")
	// ErrInvalid means an edit's data is not a valid card or lorebook.
	ErrInvalid = errors.New("invalid data")
	// ErrNameTaken means another collection has this name.
	ErrNameTaken = errors.New("name already in use")
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
		Cards:     &Entities{pool: pool, k: Card},
		Lorebooks: &Entities{pool: pool, k: Lorebook},
		pool:      pool,
		blobs:     blobs,
	}
}

// mapErr turns a missing row or a foreign key violation into ErrNotFound.
func mapErr(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case db.IsCode(err, db.ForeignKeyViolation):
		return errors.Join(ErrNotFound, err)
	}
	return err
}

// Entities is where the library keeps kind k.
func (l *Library) Entities(k *Kind) *Entities {
	if k == Lorebook {
		return l.Lorebooks
	}
	return l.Cards
}
