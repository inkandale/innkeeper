package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/cardhash"
	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// ImportKind is the job kind that imports one app item into the library.
const ImportKind = "target.import"

// Import outcomes, in a target.import job's progress.
const (
	// OutcomeCreated: a new library item, bound to the app item.
	OutcomeCreated = "created"
	// OutcomeBound: a library item with the same content, bound to the app item.
	OutcomeBound = "bound"
	// OutcomeAlreadyBound: the app item got a binding after the job was queued.
	OutcomeAlreadyBound = "already_bound"
	// OutcomeDuplicate: every library item with this content is bound to
	// another app item on the instance. Nothing changed.
	OutcomeDuplicate = "duplicate"
	// OutcomeMissing: the app no longer has the item.
	OutcomeMissing = "missing"
)

type importPayload struct {
	RemoteID string `json:"remote_id"`
	Lorebook bool   `json:"lorebook,omitempty"`
}

// ImportResult is what importing one app item did.
type ImportResult struct {
	Outcome    string `json:"outcome"`
	CardID     int64  `json:"card_id,omitempty"`
	LorebookID int64  `json:"lorebook_id,omitempty"`
	BindingID  int64  `json:"binding_id,omitempty"`
}

// EnqueueImport queues an import of the app's unbound characters and
// lorebooks, or only those named by remoteIDs, as one bulk job. The parent's
// payload counts the items skipped because they are already bound.
func (s *Service) EnqueueImport(ctx context.Context, q *jobs.Queue, instanceID int64, remoteIDs []string) (jobs.Job, error) {
	_, info, err := s.describePull(ctx, cardKind, instanceID)
	if err != nil {
		return jobs.Job{}, err
	}
	cards, err := s.Items(ctx, instanceID)
	if err != nil {
		return jobs.Job{}, err
	}
	var books []Item
	if info.Lorebooks == protocol.LorebooksStandalone {
		if books, err = s.LorebookItems(ctx, instanceID); err != nil {
			return jobs.Job{}, err
		}
	}
	var want map[string]bool
	if remoteIDs != nil {
		want = map[string]bool{}
		for _, id := range remoteIDs {
			want[id] = true
		}
	}
	var children []jobs.Spec
	alreadyBound := 0
	add := func(items []Item, lorebook bool) {
		for _, it := range items {
			if want != nil && !want[it.RemoteID] {
				continue
			}
			delete(want, it.RemoteID)
			if it.BindingID != nil {
				alreadyBound++
				continue
			}
			children = append(children, jobs.Spec{Kind: ImportKind, InstanceID: &instanceID,
				Payload: importPayload{RemoteID: it.RemoteID, Lorebook: lorebook}})
		}
	}
	add(books, true)
	add(cards, false)
	for id := range want {
		return jobs.Job{}, fmt.Errorf("%w: the app has no item %q", library.ErrInvalid, id)
	}
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: bulk.Kind, InstanceID: &instanceID,
		Payload: map[string]any{"action": ImportKind, "already_bound": alreadyBound}}, children)
}

// RunImport is the jobs.Handler for ImportKind. Running it again is safe: an
// item already bound reports already_bound.
func (s *Service) RunImport(ctx context.Context, r *jobs.Run) error {
	if r.InstanceID == nil {
		return errors.New("import job has no plugin instance")
	}
	var p importPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("import payload: %w", err)
	}
	var res ImportResult
	var err error
	if p.Lorebook {
		res, err = s.ImportLorebook(ctx, *r.InstanceID, p.RemoteID)
	} else {
		res, err = s.ImportCard(ctx, *r.InstanceID, p.RemoteID)
	}
	var startErr *pluginhost.StartError
	switch {
	case err == nil:
	case errors.As(err, &startErr):
		return err
	case plugins.Retryable(err):
		return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
	default:
		return err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	r.SetProgress(raw)
	return nil
}

// ImportCard takes the app's character into the library, with the lorebooks
// the app links to it, and binds it.
func (s *Service) ImportCard(ctx context.Context, instanceID int64, remoteID string) (ImportResult, error) {
	inst, info, err := s.describePull(ctx, cardKind, instanceID)
	if err != nil {
		return ImportResult{}, err
	}
	if res, ok, err := s.boundItem(ctx, cardKind, instanceID, remoteID); ok || err != nil {
		return res, err
	}
	got, err := s.getCard(ctx, inst, protocol.TargetGetParams{RemoteID: remoteID, Avatar: true})
	if remoteNotFound(err) {
		return ImportResult{Outcome: OutcomeMissing}, nil
	}
	if err != nil {
		return ImportResult{}, err
	}
	_, data, _, err := library.SplitCard(got.Card)
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: %s: %w", ErrBadReply, protocol.MethodTargetGet, err)
	}
	// Lorebooks go first, so a card's links don't depend on which child runs first.
	var lorebooks []int64
	if info.Lorebooks == protocol.LorebooksStandalone {
		for _, id := range got.LorebookRemoteIDs {
			res, err := s.ImportLorebook(ctx, instanceID, id)
			if err != nil {
				return ImportResult{}, fmt.Errorf("lorebook %s: %w", id, err)
			}
			if res.LorebookID != 0 {
				lorebooks = append(lorebooks, res.LorebookID)
			}
		}
	}
	plugin := inst.Manifest.Name
	return s.place(ctx, cardKind, instanceID, remoteID, data,
		func() (int64, bool, error) {
			return s.lib.ImportCard(ctx, plugin+":"+remoteID, "plugin:"+plugin, got.Card, got.Avatar)
		},
		// Only a new card takes the app's links; a matched one keeps its own.
		func(cardID int64) error {
			for _, lb := range lorebooks {
				if err := s.lib.LinkLorebook(ctx, cardID, lb); err != nil {
					return err
				}
			}
			return nil
		})
}

// ImportLorebook takes the app's lorebook into the library and binds it.
func (s *Service) ImportLorebook(ctx context.Context, instanceID int64, remoteID string) (ImportResult, error) {
	inst, _, err := s.describePull(ctx, lorebookKind, instanceID)
	if err != nil {
		return ImportResult{}, err
	}
	if res, ok, err := s.boundItem(ctx, lorebookKind, instanceID, remoteID); ok || err != nil {
		return res, err
	}
	var got protocol.TargetGetLorebookResult
	err = s.plugins.Call(ctx, inst, protocol.MethodTargetGetLorebook, protocol.TargetGetParams{RemoteID: remoteID}, &got)
	if remoteNotFound(err) {
		return ImportResult{Outcome: OutcomeMissing}, nil
	}
	if err != nil {
		return ImportResult{}, err
	}
	var meta struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(got.Lorebook, &meta); err != nil {
		return ImportResult{}, fmt.Errorf("%w: %s: %w", ErrBadReply, protocol.MethodTargetGetLorebook, err)
	}
	plugin := inst.Manifest.Name
	return s.place(ctx, lorebookKind, instanceID, remoteID, got.Lorebook,
		func() (int64, bool, error) {
			return s.lib.Lorebooks.Import(ctx, plugin+":"+remoteID, cmp.Or(meta.Name, "lorebook"),
				library.Edit{Data: got.Lorebook, Source: "plugin:" + plugin, Message: "Imported"})
		}, nil)
}

// boundItem reports the binding that already uses the app item, if any.
func (s *Service) boundItem(ctx context.Context, k kind, instanceID int64, remoteID string) (ImportResult, bool, error) {
	var bindingID int64
	var id *int64
	err := s.pool.QueryRow(ctx, `SELECT id, `+k.col+` FROM binding WHERE instance_id = $1 AND remote_id = $2`,
		instanceID, remoteID).Scan(&bindingID, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ImportResult{}, false, nil
	}
	if err != nil {
		return ImportResult{}, false, err
	}
	res := ImportResult{Outcome: OutcomeAlreadyBound, BindingID: bindingID}
	if id != nil {
		res.set(k, *id)
	}
	return res, true, nil
}

func (r *ImportResult) set(k kind, id int64) {
	if k.isLorebook() {
		r.LorebookID = id
	} else {
		r.CardID = id
	}
}

// place finds the library item with data's content, or creates it, and
// binds it to the app item. created runs on an item create made.
func (s *Service) place(ctx context.Context, k kind, instanceID int64, remoteID string, data json.RawMessage,
	create func() (int64, bool, error), created func(int64) error) (ImportResult, error) {
	hash, err := cardhash.ContentHash(data)
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: %s: %w", ErrBadReply, k.get, err)
	}
	// Without the lock, two app items with the same content, imported at
	// once, would both miss the lookup and make two library items.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"import "+k.name+" "+hash); err != nil {
		return ImportResult{}, err
	}
	// Another child may have imported this same item while this one waited.
	if res, ok, err := s.boundItem(ctx, k, instanceID, remoteID); ok || err != nil {
		return res, err
	}
	rows, err := tx.Query(ctx, `SELECT e.id, EXISTS (SELECT 1 FROM binding b WHERE b.instance_id = $2 AND b.`+k.col+` = e.id)
		FROM `+k.table+` e JOIN `+k.revTable+` r ON r.id = e.head_revision_id
		WHERE r.content_hash = $1 ORDER BY e.archived_at IS NOT NULL, e.id`, hash, instanceID)
	if err != nil {
		return ImportResult{}, err
	}
	var id, dup, rowID int64
	var rowBound bool
	if _, err := pgx.ForEachRow(rows, []any{&rowID, &rowBound}, func() error {
		switch {
		case !rowBound && id == 0:
			id = rowID
		case rowBound && dup == 0:
			dup = rowID
		}
		return nil
	}); err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{Outcome: OutcomeBound}
	switch {
	case id != 0:
	case dup != 0:
		res.Outcome = OutcomeDuplicate
		res.set(k, dup)
		return res, nil
	default:
		var isNew bool
		if id, isNew, err = create(); err != nil {
			return ImportResult{}, err
		}
		if isNew {
			res.Outcome = OutcomeCreated
			if created != nil {
				if err := created(id); err != nil {
					return ImportResult{}, err
				}
			}
		}
	}
	res.set(k, id)
	b, err := s.bind(ctx, k, instanceID, id, remoteID)
	if errors.Is(err, ErrBound) {
		// Either the app item was bound meanwhile, or create returned an
		// item, imported earlier from this app item, that is bound to another.
		if bres, ok, err := s.boundItem(ctx, k, instanceID, remoteID); ok || err != nil {
			return bres, err
		}
		res.Outcome = OutcomeDuplicate
		return res, nil
	}
	if err != nil {
		return ImportResult{}, err
	}
	res.BindingID = b.ID
	return res, tx.Commit(ctx)
}
