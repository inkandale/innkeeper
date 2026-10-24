// Package bulk holds bulk actions: a parent job with one child job per card,
// the export job, and the job that deletes a library.
package bulk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/protocol"
)

// Job kinds.
const (
	Kind                = "bulk"
	ArchiveKind         = "card.archive"
	TagKind             = "card.tag"
	CollectKind         = "card.collect"
	LorebookArchiveKind = "lorebook.archive"
	DeleteLibraryKind   = "library.delete"
)

// DeleteLibraryPayload is a DeleteLibraryKind job's payload.
type DeleteLibraryPayload struct {
	Library int64 `json:"library"`
}

// DeleteLibrary is the jobs.Handler for DeleteLibraryKind. Its progress
// counts the cards and lorebooks deleted.
func DeleteLibrary(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p DeleteLibraryPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		deleted := 0
		err := lib.DeleteLibrary(ctx, p.Library, func(k *library.Kind, n int) {
			deleted += n
			if k == library.Card {
				r.AddCardsChanged(n)
			}
			r.SetProgress(fmt.Appendf(nil, `{"deleted": %d}`, deleted))
		})
		// A run that finished before its job was marked done finds nothing left.
		if errors.Is(err, library.ErrNotFound) {
			return nil
		}
		return err
	}
}

type archivePayload struct {
	CardID    int64 `json:"card_id"`
	Archived  bool  `json:"archived"`
	Unpublish bool  `json:"unpublish,omitempty"`
}

type lorebookArchivePayload struct {
	LorebookID int64 `json:"lorebook_id"`
	Archived   bool  `json:"archived"`
	Unpublish  bool  `json:"unpublish,omitempty"`
}

// unpublisher deletes a card's or lorebook's copies from the apps it was
// pushed to.
type unpublisher interface {
	Unpublish(ctx context.Context, k *library.Kind, id int64) error
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

// EnqueueArchive archives or unarchives cards, one child job per card. With
// unpublish, archiving also deletes each card's copies from the apps it was
// pushed to.
func EnqueueArchive(ctx context.Context, q *jobs.Queue, cardIDs []int64, archived, unpublish bool) (jobs.Job, error) {
	return enqueue(ctx, q, ArchiveKind, map[string]any{"archived": archived, "unpublish": unpublish}, cardIDs,
		func(id int64) any { return archivePayload{CardID: id, Archived: archived, Unpublish: unpublish} })
}

// EnqueueLorebookArchive is EnqueueArchive for lorebooks.
func EnqueueLorebookArchive(ctx context.Context, q *jobs.Queue, ids []int64, archived, unpublish bool) (jobs.Job, error) {
	return enqueue(ctx, q, LorebookArchiveKind, map[string]any{"archived": archived, "unpublish": unpublish}, ids,
		func(id int64) any {
			return lorebookArchivePayload{LorebookID: id, Archived: archived, Unpublish: unpublish}
		})
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
//
// ponytail: counts the card even when it was already archived; compare
// before and after if the count must be exact.
func Archive(lib *library.Library, apps unpublisher) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p archivePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		if err := SetArchived(ctx, lib, apps, library.Card, p.CardID, p.Archived, p.Unpublish); err != nil {
			return err
		}
		r.AddCardsChanged(1)
		return nil
	}
}

// ArchiveLorebook is the jobs.Handler for LorebookArchiveKind.
func ArchiveLorebook(lib *library.Library, apps unpublisher) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p lorebookArchivePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		return SetArchived(ctx, lib, apps, library.Lorebook, p.LorebookID, p.Archived, p.Unpublish)
	}
}

// SetArchived archives or unarchives one card or lorebook. With unpublish, it
// deletes the app copies first, so a failed delete leaves the entity
// unarchived and a retry can finish.
func SetArchived(ctx context.Context, lib *library.Library, apps unpublisher, k *library.Kind, id int64, archived, unpublish bool) error {
	if archived && unpublish {
		if err := apps.Unpublish(ctx, k, id); err != nil {
			return err
		}
	}
	return lib.Entities(k).SetArchived(ctx, id, archived)
}

// Tag is the jobs.Handler for TagKind.
//
// ponytail: counts the card even when the edit was a no-op.
func Tag(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p tagPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		if err := lib.EditCardTags(ctx, p.CardID, p.Add, p.Remove); err != nil {
			return err
		}
		r.AddCardsChanged(1)
		return nil
	}
}

// Collect is the jobs.Handler for CollectKind.
//
// ponytail: counts the card even when the edit was a no-op.
func Collect(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p collectPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		edit := lib.AddToCollection
		if p.Remove {
			edit = lib.RemoveFromCollection
		}
		if err := edit(ctx, p.CollectionID, p.CardID); err != nil {
			return err
		}
		r.AddCardsChanged(1)
		return nil
	}
}

// PurgeKind deletes cards for good.
const PurgeKind = "card.purge"

// PurgePayload is a PurgeKind job's payload: the cards the owner confirmed.
type PurgePayload struct {
	CardIDs []int64 `json:"card_ids"`
}

// PurgeProgress is a PurgeKind job's progress. Done counts the cards deleted;
// Refused lists the cards a target app's binding kept, and Skipped counts them.
type PurgeProgress struct {
	Done    int     `json:"done"`
	Total   int     `json:"total"`
	Skipped int     `json:"skipped,omitempty"`
	Refused []int64 `json:"refused,omitempty"`
}

// purgeBatch is how many cards one transaction deletes.
const purgeBatch = 500

// Purge is the jobs.Handler for PurgeKind. A retry skips the cards an
// earlier attempt deleted.
func Purge(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p PurgePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		prog := PurgeProgress{Total: len(p.CardIDs)}
		for batch := range slices.Chunk(p.CardIDs, purgeBatch) {
			n, refused, err := lib.DeleteCards(ctx, batch)
			if err != nil {
				return err
			}
			prog.Done += n
			r.AddCardsChanged(n)
			prog.Refused = append(prog.Refused, refused...)
			prog.Skipped = len(prog.Refused)
			raw, _ := json.Marshal(prog)
			r.SetProgress(raw)
		}
		return nil
	}
}

// HideKind archives a library's duplicate cards.
const HideKind = "library.hide"

// HidePayload is a HideKind job's payload: the library's settings when the
// job was queued. Scorers give the grid score that keep best reads; Decide,
// if set, is the ai instance that confirms near duplicates.
type HidePayload struct {
	Library    int64            `json:"library"`
	Scorers    []library.Scorer `json:"scorers,omitempty"`
	Duplicates string           `json:"duplicates"`
	Distance   int              `json:"distance"`
	Keep       string           `json:"keep"`
	Decide     int64            `json:"decide,omitempty"`
}

// HideProgress counts what a HideKind job did. Rejected are near pairs the
// decision model said aren't the same card; Unchecked are those it failed
// on, which stay duplicates as their distance says.
type HideProgress struct {
	Groups     int `json:"groups"`
	Duplicate  int `json:"duplicate"`
	Archived   int `json:"archived"`
	Unarchived int `json:"unarchived"`
	Checked    int `json:"checked,omitempty"`
	Rejected   int `json:"rejected,omitempty"`
	Unchecked  int `json:"unchecked,omitempty"`
}

// Decider sends ai.decide to the ai instance named by id.
type Decider func(ctx context.Context, id int64, p protocol.AIDecideParams) (protocol.AIDecideResult, error)

// Hide is the jobs.Handler for HideKind.
func Hide(lib *library.Library, decide Decider) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p HidePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		cands, err := lib.HideCandidates(ctx, p.Library, p.Scorers)
		if err != nil {
			return err
		}
		var prog HideProgress
		var pairs []library.DuplicatePair
		switch p.Duplicates {
		case "exact":
			pairs, err = lib.DuplicatePairs(ctx, p.Library, -1)
		case "near":
			pairs, err = lib.DuplicatePairs(ctx, p.Library, p.Distance)
		}
		if err != nil {
			return err
		}
		if p.Decide != 0 {
			if pairs, err = confirmPairs(ctx, lib, decide, p.Decide, pairs, &prog, r); err != nil {
				return err
			}
		}
		groups := library.GroupPairs(pairs)
		hidden := library.PlanHide(cands, groups, p.Keep)
		prog.Groups, prog.Duplicate = len(groups), len(hidden)
		if prog.Archived, prog.Unarchived, err = lib.SetHidden(ctx, p.Library, hidden); err != nil {
			return err
		}
		r.AddCardsChanged(prog.Archived + prog.Unarchived)
		raw, _ := json.Marshal(prog)
		r.SetProgress(raw)
		return nil
	}
}

// sameCard is the question a decision model answers about a near pair.
var sameCard = map[string]protocol.DecideQuestion{"same": {Type: "noul",
	Instructions: "first and second are the same character card, perhaps with small edits, fixes, or formatting changes."}}

// confirmPairs asks the decision model about each near pair and keeps those
// it says are the same card. Exact pairs need no asking; a pair it fails on
// stays.
func confirmPairs(ctx context.Context, lib *library.Library, decide Decider, id int64, pairs []library.DuplicatePair,
	prog *HideProgress, r *jobs.Run) ([]library.DuplicatePair, error) {
	states := map[int64]json.RawMessage{}
	state := func(card int64) (json.RawMessage, error) {
		if s, ok := states[card]; ok {
			return s, nil
		}
		e, err := lib.Cards.Get(ctx, card)
		if err != nil {
			return nil, err
		}
		rev, err := lib.Cards.Revision(ctx, e.HeadRevisionID)
		if err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal(rev.Data, &data); err != nil {
			return nil, err
		}
		kept := map[string]any{}
		for _, k := range []string{"name", "description", "personality", "scenario", "first_mes"} {
			kept[k] = data[k]
		}
		states[card], err = json.Marshal(kept)
		return states[card], err
	}
	near := 0
	for _, pair := range pairs {
		if !pair.Exact {
			near++
		}
	}
	var out []library.DuplicatePair
	for _, pair := range pairs {
		if pair.Exact {
			out = append(out, pair)
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a, err := state(pair.A)
		if err != nil {
			return nil, err
		}
		b, err := state(pair.B)
		if err != nil {
			return nil, err
		}
		both, _ := json.Marshal(map[string]json.RawMessage{"first": a, "second": b})
		res, err := decide(ctx, id, protocol.AIDecideParams{State: both, Questions: sameCard})
		prog.Checked++
		switch ans, ok := res.Answers["same"]; {
		case err != nil || !ok || ans.Noul == nil:
			prog.Unchecked++
			out = append(out, pair)
		case *ans.Noul >= 0.5:
			out = append(out, pair)
		default:
			prog.Rejected++
		}
		if prog.Checked%20 == 0 || prog.Checked == near {
			raw, _ := json.Marshal(map[string]int{"done": prog.Checked, "total": near})
			r.SetProgress(raw)
		}
	}
	return out, nil
}
