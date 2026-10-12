package targets

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// syncLock is the advisory lock a sync or reconcile run holds for its whole
// run. Resolves and pushes take it shared, so they wait for a run instead of
// deciding from what the run is about to change.
const syncLock = "innkeeper-sync"

// syncRun marks a ctx as belonging to the run that holds syncLock.
type syncRun struct{}

// lockSync takes syncLock on a connection of its own and returns ctx marked
// as the run's, so the run's own resolves don't wait on it. A ctx that
// already holds it is returned as is.
//
// ponytail: one more pooled connection per run; the worker's pool allows
// two per job.
func (s *Service) lockSync(ctx context.Context) (context.Context, func(), error) {
	if ctx.Value(syncRun{}) != nil {
		return ctx, func() {}, nil
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return ctx, nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, syncLock); err != nil {
		conn.Release()
		return ctx, nil, err
	}
	release := func() {
		// A session lock outlives the run on a pooled connection; drop one that can't unlock.
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, syncLock); err != nil {
			_ = conn.Conn().Close(context.Background())
		}
		conn.Release()
	}
	return context.WithValue(ctx, syncRun{}, true), release, nil
}

// waitForSync makes tx wait for a running sync, unless ctx is that run.
func waitForSync(ctx context.Context, tx pgx.Tx) error {
	if ctx.Value(syncRun{}) != nil {
		return nil
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 0))`, syncLock)
	return err
}
