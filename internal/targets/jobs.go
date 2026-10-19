package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
)

// Job kinds. A push job's instance is the target; a scan and a source check have none.
const (
	PushKind = "target.push"
	ScanKind = "target.scan"
	// SourceCheckKind follows and checks one library's site sources; see CheckSources.
	SourceCheckKind = "target.source-check"
)

// pushPayload names a card or a lorebook.
type pushPayload struct {
	CardID     int64 `json:"card_id,omitempty"`
	LorebookID int64 `json:"lorebook_id,omitempty"`
	NewCopy    bool  `json:"new_copy,omitempty"`
	// LastPush is the binding's latest push id when the job was queued; nil
	// if the card wasn't bound. A run that finds another one knows an
	// earlier run, or another push, already pushed.
	LastPush *int64 `json:"last_push,omitempty"`
	// AppHash is the hash of the app copy a query push may replace.
	AppHash string `json:"app_hash,omitempty"`
	// CreateOnly means a query push saw no app copy, so it may only create one.
	CreateOnly bool `json:"create_only,omitempty"`
}

// EnqueuePush queues a push of one card or lorebook.
func (s *Service) EnqueuePush(ctx context.Context, q *jobs.Queue, k *library.Kind, instanceID, id int64, newCopy bool) (jobs.Job, error) {
	p := pushPayload{NewCopy: newCopy}
	err := s.pool.QueryRow(ctx, `SELECT max(p.id) FROM binding b JOIN push p ON p.binding_id = b.id
		WHERE b.instance_id = $1 AND b.`+k.FK+` = $2`, instanceID, id).Scan(&p.LastPush)
	if err != nil {
		return jobs.Job{}, err
	}
	if k == library.Lorebook {
		p.LorebookID = id
	} else {
		p.CardID = id
	}
	return q.Enqueue(ctx, jobs.Spec{Kind: PushKind, InstanceID: &instanceID, Payload: p})
}

// EnqueueBulkPush queues a push of each card or lorebook, as one bulk job.
// With confirm from PreviewBulkPush, it returns a *StalePreviewError if the
// preview would differ now, and each push may replace the app copy the
// preview counted. Without it, each may replace only a copy unchanged since
// its last push or resolve. A push whose app copy differs fails with
// ErrAppChanged.
func (s *Service) EnqueueBulkPush(ctx context.Context, q *jobs.Queue, lk *library.Kind, instanceID int64, ids []int64, newCopy bool, confirm string) (jobs.Job, error) {
	k := targetKind(lk)
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	preview, bound, err := s.bulkPreview(ctx, k, instanceID, ids)
	if err != nil {
		return jobs.Job{}, err
	}
	if confirm != "" && confirm != preview.Confirm {
		return jobs.Job{}, &StalePreviewError{Preview: preview}
	}
	children := make([]jobs.Spec, len(ids))
	for i, id := range ids {
		p := pushPayload{NewCopy: newCopy}
		if k.isLorebook() {
			p.LorebookID = id
		} else {
			p.CardID = id
		}
		t, ok := bound[id]
		if ok {
			p.LastPush = &t.lastPush
			p.AppHash = t.AppHash
			if confirm == "" && t.AppHash != "" {
				p.AppHash = t.accepted
			}
		}
		p.CreateOnly = p.AppHash == ""
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
	k, id, key := cardKind, p.CardID, fmt.Sprintf("%d:%d:%d", *r.InstanceID, p.CardID, r.ID)
	if p.LorebookID != 0 {
		k, id, key = lorebookKind, p.LorebookID, fmt.Sprintf("%d:lorebook:%d:%d", *r.InstanceID, p.LorebookID, r.ID)
	}
	b, err := s.push(ctx, k, *r.InstanceID, id, p.NewCopy, key, &pushExpect{lastPush: p.LastPush, appHash: p.AppHash, createOnly: p.CreateOnly})
	switch {
	case errors.Is(err, errAlreadyPushed):
		var bindingID int64
		if err := s.pool.QueryRow(ctx, `SELECT id FROM binding WHERE instance_id = $1 AND `+k.FK+` = $2 AND NOT imported`,
			*r.InstanceID, id).Scan(&bindingID); err != nil {
			return err
		}
		return skipPush(r, bindingID)
	case err == nil:
	case errors.Is(err, ErrUncertain):
		// Final even on shutdown: a re-run could make a second copy.
		return fmt.Errorf("%w: %w", jobs.ErrFinal, err)
	default:
		return plugins.JobErr(err)
	}
	raw, err := json.Marshal(map[string]any{"binding_id": b.ID, "remote_id": b.RemoteID})
	if err != nil {
		return err
	}
	if p.LorebookID == 0 {
		r.AddCardsChanged(1)
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

// SourceCheckPayload is a SourceCheckKind job's payload.
type SourceCheckPayload struct {
	// Library is the library to check; 0 means library.DefaultLibrary.
	Library int64 `json:"library,omitempty"`
}

// RunSourceCheck is the jobs.Handler for SourceCheckKind.
func (s *Service) RunSourceCheck(ctx context.Context, r *jobs.Run) error {
	var p SourceCheckPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("source check payload: %w", err)
	}
	res, err := s.CheckSources(ctx, cmp.Or(p.Library, library.DefaultLibrary), func(p ScanProgress) {
		raw, _ := json.Marshal(p)
		r.SetProgress(raw)
	})
	r.AddCardsChanged(res.cardsApplied)
	return err
}
