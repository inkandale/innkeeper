package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

// Job kinds. A push job's instance is the target; a scan has none.
const (
	PushKind = "target.push"
	ScanKind = "target.scan"
)

type pushPayload struct {
	CardID  int64 `json:"card_id"`
	NewCopy bool  `json:"new_copy,omitempty"`
}

// EnqueuePush queues a push of one card.
func EnqueuePush(ctx context.Context, q *jobs.Queue, instanceID, cardID int64, newCopy bool) (jobs.Job, error) {
	return q.Enqueue(ctx, jobs.Spec{Kind: PushKind, InstanceID: &instanceID, Payload: pushPayload{cardID, newCopy}})
}

// EnqueueBulkPush queues a push of each card, as one bulk job.
func EnqueueBulkPush(ctx context.Context, q *jobs.Queue, instanceID int64, cardIDs []int64, newCopy bool) (jobs.Job, error) {
	children := make([]jobs.Spec, len(cardIDs))
	for i, id := range cardIDs {
		children[i] = jobs.Spec{Kind: PushKind, InstanceID: &instanceID, Payload: pushPayload{id, newCopy}}
	}
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: bulk.Kind, InstanceID: &instanceID,
		Payload: map[string]any{"action": PushKind, "new_copy": newCopy}}, children)
}

// RunPush is the jobs.Handler for PushKind.
func (s *Service) RunPush(ctx context.Context, r *jobs.Run) error {
	if r.InstanceID == nil {
		return errors.New("push job has no plugin instance")
	}
	var p pushPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("push payload: %w", err)
	}
	b, err := s.Push(ctx, *r.InstanceID, p.CardID, p.NewCopy)
	if plugins.Retryable(err) {
		return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"binding_id": b.ID, "remote_id": b.RemoteID})
	r.SetProgress(raw)
	return err
}

// RunScan is the jobs.Handler for ScanKind.
func (s *Service) RunScan(ctx context.Context, r *jobs.Run) error {
	_, err := s.Scan(ctx, func(p ScanProgress) {
		raw, _ := json.Marshal(p)
		r.SetProgress(raw)
	})
	return err
}
