package library

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GarbageKind is the job kind that runs CollectGarbage.
const GarbageKind = "blob.gc"

// CollectGarbage deletes blobs in this library's store that no revision
// or waiting import update references, and leftover files in its tmp and staging folders. It only
// touches what is older than grace, which keeps it clear of imports in
// flight. It returns how many blobs and leftover files it deleted.
//
// ponytail: a blob re-ingested while it is being deleted can lose its file;
// the grace period makes that rare, as the spec accepts.
func (l *Library) CollectGarbage(ctx context.Context, grace time.Duration) (blobs, leftovers int, err error) {
	// A restore holds this lock while it writes blob files whose rows aren't
	// committed yet, which would look unreferenced here.
	c, err := l.pool.Acquire(ctx)
	if err != nil {
		return 0, 0, err
	}
	conn := c.Hijack() // closing it releases the lock, whatever happens
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('innkeeper-restore'))`); err != nil {
		return 0, 0, err
	}
	rows, err := conn.Query(ctx, `DELETE FROM blob b
		WHERE store = $1 AND created_at < now() - make_interval(secs => $2)
			AND NOT EXISTS (SELECT 1 FROM card_revision WHERE avatar_blob = b.sha256)
			AND NOT EXISTS (SELECT 1 FROM lorebook_revision WHERE avatar_blob = b.sha256)
			AND NOT EXISTS (SELECT 1 FROM binding WHERE seen_avatar = b.sha256)
		RETURNING sha256`, l.blobs.Name, grace.Seconds())
	if err != nil {
		return 0, 0, err
	}
	shas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, 0, err
	}
	// The rows are already gone, so a file skipped here is never retried.
	var errs []error
	for _, sha := range shas {
		if err := l.blobs.Remove(sha); err != nil {
			errs = append(errs, err)
			continue
		}
		blobs++
	}
	leftovers, err = l.blobs.RemoveStale(time.Now().Add(-grace))
	return blobs, leftovers, errors.Join(append(errs, err)...)
}
