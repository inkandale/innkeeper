package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
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

// EnqueueSync queues a sync, or returns the one already queued or running.
func (s *Service) EnqueueSync(ctx context.Context, q *jobs.Queue, p SyncParams) (jobs.Job, error) {
	p.Apply = cmp.Or(p.Apply, ApplyObvious)
	if p.Apply != ApplyObvious && p.Apply != ApplyNone {
		return jobs.Job{}, fmt.Errorf("%w: apply must be obvious or none", library.ErrInvalid)
	}
	if p.InstanceID != 0 {
		if _, _, err := s.describeReconcile(ctx, cardKind, p.InstanceID); err != nil {
			return jobs.Job{}, err
		}
	}
	// ponytail: a start joins any running sync, even one limited to another app.
	return q.EnqueueOnce(ctx, jobs.Spec{Kind: SyncKind, Payload: p})
}

// RunSync is the jobs.Handler for SyncKind.
func (s *Service) RunSync(ctx context.Context, r *jobs.Run) error {
	var p SyncParams
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("sync payload: %w", err)
	}
	report := func(res SyncResult) {
		raw, _ := json.Marshal(res)
		r.SetProgress(raw)
	}
	res, err := s.Sync(ctx, p, report)
	if err != nil {
		return err
	}
	report(res)
	return nil
}

// Sync reconciles every target app that can pull, one at a time, or only
// p.InstanceID, holding the sync lock throughout. Each app's run is recorded
// as a finished reconcile job, which the review queue reads. An app that
// fails is recorded as a failed job and the sync goes on. progress, if set,
// hears after each app.
func (s *Service) Sync(ctx context.Context, p SyncParams, progress func(SyncResult)) (SyncResult, error) {
	res := SyncResult{Apps: []SyncApp{}}
	ids, err := s.syncApps(ctx, p.InstanceID)
	if err != nil {
		return res, err
	}
	ctx, release, err := s.lockSync(ctx)
	if err != nil {
		return res, err
	}
	defer release()
	q := jobs.New(s.pool)
	for _, id := range ids {
		params := ReconcileParams{InstanceID: id, Apply: cmp.Or(p.Apply, ApplyObvious)}
		snap, err := s.Reconcile(ctx, params, nil)
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		app := SyncApp{InstanceID: id}
		var raw json.RawMessage
		if err != nil {
			app.Error = err.Error()
		} else {
			app.Totals, params = snap.Totals, snap.Params
			if raw, err = json.Marshal(snap); err != nil {
				return res, err
			}
		}
		j, rerr := q.Record(ctx, jobs.Spec{Kind: ReconcileKind, InstanceID: &id, Payload: params}, raw, errorOrNil(app.Error))
		if rerr != nil {
			return res, rerr
		}
		app.JobID = j.ID
		res.Apps = append(res.Apps, app)
		if progress != nil {
			progress(res)
		}
	}
	return res, nil
}

// syncApps is the instances a sync visits: only, or every working
// instance whose plugin can push and pull, in ID order.
func (s *Service) syncApps(ctx context.Context, only int64) ([]int64, error) {
	if only != 0 {
		return []int64{only}, nil
	}
	insts, err := s.reg.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, in := range insts {
		if in.BrokenError == nil && s.reg.HasCapability(in.Plugin, "push") && s.reg.HasCapability(in.Plugin, "pull") {
			ids = append(ids, in.ID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func errorOrNil(msg string) error {
	if msg == "" {
		return nil
	}
	return errors.New(msg)
}
