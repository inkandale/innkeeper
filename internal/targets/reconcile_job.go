package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

// EnqueueReconcile queues a reconcile run. Bad params and a target that
// can't pull or push are refused here, so no job is queued for them.
func (s *Service) EnqueueReconcile(ctx context.Context, q *jobs.Queue, p ReconcileParams) (jobs.Job, error) {
	p, err := withDefaults(p)
	if err != nil {
		return jobs.Job{}, err
	}
	if _, _, err := s.describeApp(ctx, p.InstanceID); err != nil {
		return jobs.Job{}, err
	}
	return q.Enqueue(ctx, jobs.Spec{Kind: ReconcileKind, InstanceID: &p.InstanceID, Payload: p})
}

// RunReconcile is the jobs.Handler for ReconcileKind. It only reads, so a
// retry starts over. A failed run keeps its last count, never a partial
// snapshot.
func (s *Service) RunReconcile(ctx context.Context, r *jobs.Run) error {
	if r.InstanceID == nil {
		return errors.New("reconcile job has no plugin instance")
	}
	var p ReconcileParams
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("reconcile payload: %w", err)
	}
	p.InstanceID = *r.InstanceID
	ctx, release, err := s.lockSync(ctx)
	if err != nil {
		return err
	}
	defer release()
	snap, err := s.Reconcile(ctx, p, func(done, total int) {
		raw, _ := json.Marshal(map[string]int{"done": done, "total": total})
		r.SetProgress(raw)
	})
	if err != nil {
		return plugins.JobErr(err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	r.SetProgress(raw)
	return nil
}
