// Package bulk holds bulk actions: a parent job with one child job per card,
// and the export job.
package bulk

import (
	"context"
	"encoding/json"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// Job kinds.
const (
	Kind                = "bulk"
	ArchiveKind         = "card.archive"
	TagKind             = "card.tag"
	CollectKind         = "card.collect"
	LorebookArchiveKind = "lorebook.archive"
)

type archivePayload struct {
	CardID   int64 `json:"card_id"`
	Archived bool  `json:"archived"`
}

type lorebookArchivePayload struct {
	LorebookID int64 `json:"lorebook_id"`
	Archived   bool  `json:"archived"`
}

type tagPayload struct {
	CardID int64    `json:"card_id"`
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
}

type collectPayload struct {
	CardID       int64 `json:"card_id"`
	CollectionID int64 `json:"collection_id"`
	Remove       bool  `json:"remove,omitempty"`
}

// enqueue queues a bulk parent with params and one child of kind per card.
func enqueue(ctx context.Context, q *jobs.Queue, kind string, params map[string]any, cardIDs []int64, payload func(int64) any) (jobs.Job, error) {
	children := make([]jobs.Spec, len(cardIDs))
	for i, id := range cardIDs {
		children[i] = jobs.Spec{Kind: kind, Payload: payload(id)}
	}
	params["action"] = kind
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: Kind, Payload: params}, children)
}

// EnqueueArchive archives or unarchives cards, one child job per card.
func EnqueueArchive(ctx context.Context, q *jobs.Queue, cardIDs []int64, archived bool) (jobs.Job, error) {
	return enqueue(ctx, q, ArchiveKind, map[string]any{"archived": archived}, cardIDs,
		func(id int64) any { return archivePayload{CardID: id, Archived: archived} })
}

// EnqueueLorebookArchive archives or unarchives lorebooks, one child job per
// lorebook.
func EnqueueLorebookArchive(ctx context.Context, q *jobs.Queue, ids []int64, archived bool) (jobs.Job, error) {
	return enqueue(ctx, q, LorebookArchiveKind, map[string]any{"archived": archived}, ids,
		func(id int64) any { return lorebookArchivePayload{LorebookID: id, Archived: archived} })
}

// EnqueueTag adds and removes tags on cards, one child job per card.
func EnqueueTag(ctx context.Context, q *jobs.Queue, cardIDs []int64, add, remove []string) (jobs.Job, error) {
	return enqueue(ctx, q, TagKind, map[string]any{"add": add, "remove": remove}, cardIDs,
		func(id int64) any { return tagPayload{CardID: id, Add: add, Remove: remove} })
}

// EnqueueCollect adds cards to a collection, or removes them, one child job
// per card.
func EnqueueCollect(ctx context.Context, q *jobs.Queue, cardIDs []int64, collectionID int64, remove bool) (jobs.Job, error) {
	return enqueue(ctx, q, CollectKind, map[string]any{"collection_id": collectionID, "remove": remove}, cardIDs,
		func(id int64) any { return collectPayload{CardID: id, CollectionID: collectionID, Remove: remove} })
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

// ArchiveLorebook is the jobs.Handler for LorebookArchiveKind.
func ArchiveLorebook(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p lorebookArchivePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		return lib.Lorebooks.SetArchived(ctx, p.LorebookID, p.Archived)
	}
}

// Tag is the jobs.Handler for TagKind.
func Tag(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p tagPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		return lib.EditCardTags(ctx, p.CardID, p.Add, p.Remove)
	}
}

// Collect is the jobs.Handler for CollectKind.
func Collect(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p collectPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		if p.Remove {
			return lib.RemoveFromCollection(ctx, p.CollectionID, p.CardID)
		}
		return lib.AddToCollection(ctx, p.CollectionID, p.CardID)
	}
}
