package library

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// CollectGarbage deletes blobs in this library's store that no revision
// references, and leftover files in its tmp and staging folders. It only
// touches what is older than grace, which keeps it clear of imports in
// flight. It returns how many blobs and leftover files it deleted.
//
// ponytail: a blob re-ingested while it is being deleted can lose its file;
// the grace period makes that rare, as the spec accepts.
func (l *Library) CollectGarbage(ctx context.Context, grace time.Duration) (blobs, leftovers int, err error) {
	rows, err := l.pool.Query(ctx, `DELETE FROM blob b
		WHERE store = $1 AND created_at < now() - make_interval(secs => $2)
			AND NOT EXISTS (SELECT 1 FROM card_revision WHERE avatar_blob = b.sha256)
			AND NOT EXISTS (SELECT 1 FROM lorebook_revision WHERE avatar_blob = b.sha256)
		RETURNING sha256`, l.blobs.Name, grace.Seconds())
	if err != nil {
		return 0, 0, err
	}
	shas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, 0, err
	}
	for _, sha := range shas {
		if err := l.blobs.Remove(sha); err != nil {
			return blobs, 0, err
		}
		blobs++
	}
	leftovers, err = l.blobs.RemoveStale(time.Now().Add(-grace))
	return blobs, leftovers, err
}
