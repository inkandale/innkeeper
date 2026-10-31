// Package library stores cards and lorebooks as append-only revisions, along
// with blobs, tags, collections, and duplicate lookups.
package library

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/db"
)

// DefaultLibrary is the library cards and lorebooks go to when no library is
// named. Migration 0029 creates it.
const DefaultLibrary int64 = 1

var (
	ErrNotFound = errors.New("not found")
	// ErrNoVectorInstance means vector_instance is unset, or names an instance that is gone or disabled.
	ErrNoVectorInstance = errors.New("semantic search needs a vector source; pick one in Settings under Plugins, Vectors from")
	// ErrNoVectors means pgvector isn't installed, so semantic search is off.
	ErrNoVectors = errors.New("semantic search needs pgvector; see docs/running.md")
	// ErrConflict means the write's base revision is no longer the head.
	ErrConflict  = errors.New("base revision is not the head")
	ErrSlugTaken = errors.New("slug already in use")
	// ErrInvalid means an edit's data is not a valid card or lorebook.
	ErrInvalid = errors.New("invalid data")
	// ErrNameTaken means another collection or library has this name.
	ErrNameTaken = errors.New("name already in use")
	// ErrVariantCycle means a variant link would make an entity its own ancestor.
	ErrVariantCycle = errors.New("a variant can't be its own ancestor")
)

// Library is the core's card and lorebook store.
type Library struct {
	Cards     *Entities
	Lorebooks *Entities

	pool  *pgxpool.Pool
	blobs *blobstore.Store

	vectorsOnce sync.Once
	vectors     bool
	vectorsErr  error
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

// execer is a pool or a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
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
