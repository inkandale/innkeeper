// Package bulk holds bulk actions: a parent job with one child job per card.
package bulk

import (
	"context"
	"encoding/json"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// Job kinds.
const (
	Kind        = "bulk"
	ArchiveKind = "card.archive"
)

type archivePayload struct {
	CardID   int64 `json:"card_id"`
	Archived bool  `json:"archived"`
}

// EnqueueArchive archives or unarchives cards, one child job per card.
func EnqueueArchive(ctx context.Context, q *jobs.Queue, cardIDs []int64, archived bool) (jobs.Job, error) {
	children := make([]jobs.Spec, len(cardIDs))
	for i, id := range cardIDs {
		children[i] = jobs.Spec{Kind: ArchiveKind, Payload: archivePayload{CardID: id, Archived: archived}}
	}
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: Kind, Payload: map[string]any{"action": ArchiveKind, "archived": archived}}, children)
}

// Archive is the jobs.Handler for ArchiveKind.
func Archive(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p archivePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		return lib.Cards.SetArchived(ctx, p.CardID, p.Archived)
	}
}
