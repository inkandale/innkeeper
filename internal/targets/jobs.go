package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
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
	// LastPush is the binding's latest push id when the job was queued; nil
	// if the card wasn't bound. A run that finds another one knows an
	// earlier run, or another push, already pushed.
	LastPush *int64 `json:"last_push,omitempty"`
}

// EnqueuePush queues a push of one card.
func (s *Service) EnqueuePush(ctx context.Context, q *jobs.Queue, instanceID, cardID int64, newCopy bool) (jobs.Job, error) {
	var v *int64
	err := s.pool.QueryRow(ctx, `SELECT max(p.id) FROM binding b JOIN push p ON p.binding_id = b.id
		WHERE b.instance_id = $1 AND b.card_id = $2`, instanceID, cardID).Scan(&v)
	if err != nil {
		return jobs.Job{}, err
	}
	return q.Enqueue(ctx, jobs.Spec{Kind: PushKind, InstanceID: &instanceID, Payload: pushPayload{cardID, newCopy, v}})
}

// EnqueueBulkPush queues a push of each card, as one bulk job.
func (s *Service) EnqueueBulkPush(ctx context.Context, q *jobs.Queue, instanceID int64, cardIDs []int64, newCopy bool) (jobs.Job, error) {
	cardIDs = slices.Clone(cardIDs)
	slices.Sort(cardIDs)
	cardIDs = slices.Compact(cardIDs)
	rows, err := s.pool.Query(ctx, `SELECT b.card_id, max(p.id) FROM binding b JOIN push p ON p.binding_id = b.id
		WHERE b.instance_id = $1 AND b.card_id = ANY($2) GROUP BY b.card_id`, instanceID, cardIDs)
	if err != nil {
		return jobs.Job{}, err
	}
	lastPush := map[int64]int64{}
	var card, last int64
	if _, err := pgx.ForEachRow(rows, []any{&card, &last}, func() error {
		lastPush[card] = last
		return nil
	}); err != nil {
		return jobs.Job{}, err
	}
	children := make([]jobs.Spec, len(cardIDs))
	for i, id := range cardIDs {
		p := pushPayload{CardID: id, NewCopy: newCopy}
		if v, ok := lastPush[id]; ok {
			p.LastPush = &v
		}
		children[i] = jobs.Spec{Kind: PushKind, InstanceID: &instanceID, Payload: p}
	}
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: bulk.Kind, InstanceID: &instanceID,
		Payload: map[string]any{"action": PushKind, "new_copy": newCopy}}, children)
}

// RunPush is the jobs.Handler for PushKind. A job can run twice (shutdown,
// a lost lease, a retry), so it skips a push an earlier run already made.
func (s *Service) RunPush(ctx context.Context, r *jobs.Run) error {
	if r.InstanceID == nil {
		return errors.New("push job has no plugin instance")
	}
	var p pushPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("push payload: %w", err)
	}
	b, err := s.push(ctx, *r.InstanceID, p.CardID, p.NewCopy, &pushExpect{p.LastPush})
	var startErr *pluginhost.StartError
	switch {
	case errors.Is(err, errAlreadyPushed):
		var id int64
		if err := s.pool.QueryRow(ctx, `SELECT id FROM binding WHERE instance_id = $1 AND card_id = $2`,
			*r.InstanceID, p.CardID).Scan(&id); err != nil {
			return err
		}
		return skipPush(r, id)
	case err == nil:
	case errors.Is(err, ErrUncertain):
		// Final even on shutdown: a re-run could make a second copy.
		return fmt.Errorf("%w: %w", jobs.ErrFinal, err)
	case errors.As(err, &startErr):
		return err
	case plugins.Retryable(err):
		return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
	default:
		return err
	}
	raw, err := json.Marshal(map[string]any{"binding_id": b.ID, "remote_id": b.RemoteID})
	if err != nil {
		return err
	}
	r.SetProgress(raw)
	return nil
}

func skipPush(r *jobs.Run, bindingID int64) error {
	raw, err := json.Marshal(map[string]any{"binding_id": bindingID, "skipped": "already pushed"})
	if err != nil {
		return err
	}
	r.SetProgress(raw)
	return nil
}

// RunScan is the jobs.Handler for ScanKind.
func (s *Service) RunScan(ctx context.Context, r *jobs.Run) error {
	_, err := s.Scan(ctx, func(p ScanProgress) {
		raw, _ := json.Marshal(p)
		r.SetProgress(raw)
	})
	return err
}
