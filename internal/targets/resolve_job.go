package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
)

// ResolveKind is the job kind that applies one resolution. Its progress has
// the outcome: the action on success; stale, conflict, or failed otherwise.
const ResolveKind = "target.resolve"

// resolveProgress is a resolve job's progress.
type resolveProgress struct {
	Outcome string         `json:"outcome"`
	Result  *ResolveResult `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`
	// Stale is the fresh comparison for a stale item.
	Stale *StaleError `json:"stale,omitempty"`
}

// EnqueueResolve checks each resolution's shape, then queues a bulk job with
// one child per resolution. A child that fails doesn't stop the others.
func (s *Service) EnqueueResolve(ctx context.Context, q *jobs.Queue, rs []Resolution) (jobs.Job, error) {
	children := make([]jobs.Spec, len(rs))
	for i, r := range rs {
		k := kindNamed(r.Kind)
		if r.InstanceID == 0 {
			return jobs.Job{}, fmt.Errorf("%w: resolution %d: instance_id is required", library.ErrInvalid, i)
		}
		if err := validateResolution(r, k); err != nil {
			return jobs.Job{}, fmt.Errorf("resolution %d: %w", i, err)
		}
		children[i] = jobs.Spec{Kind: ResolveKind, InstanceID: &r.InstanceID, Payload: r}
	}
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: bulk.Kind, Payload: map[string]any{"action": ResolveKind}}, children)
}

// RunResolve is the jobs.Handler for ResolveKind. A resolve commits as a
// whole or not at all, so a retry starts over; a stale or conflicting item
// isn't retried, since only the owner can choose again.
func (s *Service) RunResolve(ctx context.Context, run *jobs.Run) error {
	if run.InstanceID == nil {
		return errors.New("resolve job has no plugin instance")
	}
	var r Resolution
	if err := json.Unmarshal(run.Payload, &r); err != nil {
		return fmt.Errorf("resolve payload: %w", err)
	}
	r.InstanceID = *run.InstanceID
	res, err := s.resolve(ctx, kindNamed(r.Kind), r, resolveOpts{})
	if err == nil && res.RevisionID != 0 && !kindNamed(r.Kind).isLorebook() {
		run.AddCardsChanged(1)
	}
	p := resolveProgress{Outcome: r.Action, Result: &res}
	var stale *StaleError
	var startErr *pluginhost.StartError
	switch {
	case err == nil:
	case errors.As(err, &startErr), plugins.Retryable(err):
		return plugins.JobErr(err)
	case errors.As(err, &stale):
		p = resolveProgress{Outcome: "stale", Stale: stale}
	case errors.Is(err, library.ErrConflict):
		p = resolveProgress{Outcome: "conflict"}
	default:
		p = resolveProgress{Outcome: "failed"}
	}
	if err != nil {
		p.Error = err.Error()
	}
	raw, merr := json.Marshal(p)
	if merr != nil {
		return merr
	}
	run.SetProgress(raw)
	return err
}
