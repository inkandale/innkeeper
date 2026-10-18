package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/internal/library"
)

// failuresShown is how many checks of a followed source must fail in a row
// before the queue shows it, so one bad hour of a site stays quiet.
const failuresShown = 3

// Changes lists every card some app needs the owner for, the cards and
// lorebooks whose followed source changed, and the app items no card matches,
// from each app's latest finished reconcile and the binding statuses. An entry
// the owner settled after that reconcile is left out.
//
// ponytail: built from every app's latest snapshot on each call; store the
// rows if a library outgrows that.
func (s *Service) Changes(ctx context.Context, f ChangesFilter) (Changes, error) {
	out := Changes{Rows: []ChangeRow{}, Counts: map[int64]map[string]int{}}
	names := map[int64]string{}
	insts, err := s.reg.ListInstances(ctx)
	if err != nil {
		return out, err
	}
	for _, in := range insts {
		names[in.ID] = in.Label
	}
	// One row per library entry, by kind and ID.
	type key struct {
		book bool
		id   int64
	}
	entries := map[key]*ChangeRow{}
	var orphans []ChangeRow
	seen := map[[3]int64]bool{} // kind (1 for lorebook), entry, and instance with a snapshot change
	kindOf := func(book bool) string {
		if book {
			return "lorebook"
		}
		return "card"
	}
	// count tallies c and says whether the filter keeps it.
	count := func(c AppChange) bool {
		if out.Counts[c.InstanceID] == nil {
			out.Counts[c.InstanceID] = map[string]int{}
		}
		out.Counts[c.InstanceID][c.State]++
		return (f.InstanceID == 0 || c.InstanceID == f.InstanceID) && (f.State == "" || c.State == f.State)
	}
	// add files c under the library entry id of its kind, or as an orphan for 0.
	add := func(book bool, id int64, c AppChange) {
		kept := count(c)
		if id != 0 {
			seen[[3]int64{boolInt(book), id, c.InstanceID}] = true
		}
		if !kept {
			return
		}
		if id == 0 {
			orphans = append(orphans, ChangeRow{Kind: kindOf(book), Name: fmt.Sprintf("%s (%s only)", c.Name, names[c.InstanceID]), Apps: []AppChange{c}})
			return
		}
		r := entries[key{book, id}]
		if r == nil {
			r = &ChangeRow{Kind: kindOf(book)}
			if book {
				r.LorebookID = id
			} else {
				r.CardID = id
			}
			entries[key{book, id}] = r
		}
		r.Apps = append(r.Apps, c)
	}

	// Each live binding's entry and accepted copy, by kind, instance, and remote
	// ID, to drop entries settled since the snapshot; and the entries bound now.
	type live struct {
		id   int64
		hash string
	}
	accepted := map[bool]map[int64]map[string]live{false: {}, true: {}}
	boundNow := map[[3]int64]bool{} // kind, entry, and instance
	rows, err := s.pool.Query(ctx, `SELECT instance_id, remote_id, coalesce(card_id, 0), coalesce(lorebook_id, 0), accepted_hash
		FROM binding WHERE push AND remote_state <> 'missing'`)
	if err != nil {
		return out, err
	}
	var inst, bcard, bbook int64
	var remote, hash string
	if _, err := pgx.ForEachRow(rows, []any{&inst, &remote, &bcard, &bbook, &hash}, func() error {
		book, id := bbook != 0, cmp.Or(bcard, bbook)
		if accepted[book][inst] == nil {
			accepted[book][inst] = map[string]live{}
		}
		accepted[book][inst][remote] = live{id, hash}
		boundNow[[3]int64{boolInt(book), id, inst}] = true
		return nil
	}); err != nil {
		return out, err
	}

	type snapshot struct {
		job, inst int64
		snap      Snapshot
	}
	var latest []snapshot
	snaps, err := s.pool.Query(ctx, `SELECT DISTINCT ON (instance_id) id, instance_id, progress FROM job
		WHERE kind = $1 AND status = 'succeeded' AND parent_id IS NULL AND instance_id IS NOT NULL
		ORDER BY instance_id, id DESC`, ReconcileKind)
	if err != nil {
		return out, err
	}
	var job int64
	var raw []byte
	if _, err := pgx.ForEachRow(snaps, []any{&job, &inst, &raw}, func() error {
		l := snapshot{job: job, inst: inst}
		if err := json.Unmarshal(raw, &l.snap); err != nil {
			return fmt.Errorf("job %d snapshot: %w", job, err)
		}
		latest = append(latest, l)
		return nil
	}); err != nil {
		return out, err
	}
	// classify is the queue state of snapshot item it of kind book on instance
	// inst and the library entry it belongs to, or false when it needs nothing.
	classify := func(book bool, inst int64, it ReconcileItem) (string, int64, bool) {
		acc, isBound := accepted[book][inst][it.RemoteID]
		switch {
		case it.AutoBound || it.AutoImported || it.AutoPulled:
			return "", 0, false
		case it.Verdict == VerdictError:
			// An item the app can't read has no candidates; a bound one belongs to its binding's entry.
			if isBound {
				return ChangeError, acc.id, true
			}
			if len(it.Candidates) > 0 {
				return ChangeError, it.Candidates[0].CardID, true
			}
			return ChangeError, 0, true
		case it.Verdict == VerdictBound:
			// A library-only change shows as ahead from the binding status instead.
			if !it.AppChanged || it.AppOnly || len(it.Candidates) == 0 || (isBound && acc.hash == it.AppHash) {
				return "", 0, false
			}
			return ChangeBothChanged, it.Candidates[0].CardID, true
		case isBound:
			// Bound since the snapshot.
			return "", 0, false
		}
		// An entry bound on this app since the snapshot, as a duplicate's sibling may be, is taken.
		i := slices.IndexFunc(it.Candidates, func(c Candidate) bool {
			return c.BoundTo == "" && !boundNow[[3]int64{boolInt(book), c.CardID, inst}]
		})
		if it.Verdict == VerdictNew || i < 0 {
			return ChangeNew, 0, true
		}
		return ChangeDiffers, it.Candidates[i].CardID, true
	}
	for _, l := range latest {
		// A world book linked to several characters is nested under each; it counts once.
		books := map[string]bool{}
		visit := func(book bool, it ReconcileItem) {
			if book {
				if books[it.RemoteID] {
					return
				}
				books[it.RemoteID] = true
			}
			state, id, ok := classify(book, l.inst, it)
			if !ok {
				return
			}
			add(book, id, AppChange{InstanceID: l.inst, State: state, RemoteID: it.RemoteID, Name: it.Name, BindingID: it.BindingID,
				Push: true, JobID: l.job, AppHash: it.AppHash, Suggested: it.Suggested, Error: it.Error})
		}
		for _, it := range l.snap.Items {
			visit(false, it)
			for _, nested := range it.Lorebooks {
				visit(true, nested)
			}
		}
		for _, it := range l.snap.Lorebooks {
			visit(true, it)
		}
	}

	st, err := s.pool.Query(ctx, `SELECT coalesce(b.card_id, 0), coalesce(b.lorebook_id, 0), b.instance_id, b.id, b.remote_id, s.status
		FROM binding b JOIN binding_status s ON s.binding_id = b.id
		LEFT JOIN card c ON c.id = b.card_id LEFT JOIN lorebook l ON l.id = b.lorebook_id
		WHERE coalesce(c.archived_at, l.archived_at) IS NULL AND b.push AND s.status IN ('ahead', 'missing') ORDER BY b.id`)
	if err != nil {
		return out, err
	}
	var card, book, binding int64
	var status string
	if _, err := pgx.ForEachRow(st, []any{&card, &book, &inst, &binding, &remote, &status}, func() error {
		isBook, id := book != 0, cmp.Or(card, book)
		if !seen[[3]int64{boolInt(isBook), id, inst}] {
			add(isBook, id, AppChange{InstanceID: inst, State: status, RemoteID: remote, BindingID: binding, Push: true})
		}
		return nil
	}); err != nil {
		return out, err
	}

	// Followed sources from their last check, and import updates. A source the owner removed has no
	// live source row, so it is gone from the queue before the next check unbinds it.
	fl, err := s.pool.Query(ctx, `SELECT coalesce(b.card_id, 0), coalesce(b.lorebook_id, 0), b.instance_id, b.id, b.remote_id,
			b.remote_state, coalesce(b.check_error, ''), coalesce(src.id, 0), b.seen_data
		FROM binding b LEFT JOIN card c ON c.id = b.card_id LEFT JOIN lorebook l ON l.id = b.lorebook_id
		LEFT JOIN LATERAL (SELECT s.id FROM card_source s WHERE s.card_id = b.card_id AND s.removed_at IS NULL
				AND s.kind || ':' || s.ref = b.remote_id
			UNION ALL SELECT s.id FROM lorebook_source s WHERE s.lorebook_id = b.lorebook_id AND s.removed_at IS NULL
				AND s.kind || ':' || s.ref = b.remote_id
			LIMIT 1) src ON TRUE
		WHERE NOT b.push AND NOT b.paused AND coalesce(c.archived_at, l.archived_at) IS NULL
			AND (src.id IS NOT NULL OR b.imported) AND (b.remote_state IN ('changed', 'missing') OR b.check_failures >= $1)
		ORDER BY b.id`, failuresShown)
	if err != nil {
		return out, err
	}
	var source int64
	var state, checkErr string
	var seenData []byte
	if _, err := pgx.ForEachRow(fl, []any{&card, &book, &inst, &binding, &remote, &state, &checkErr, &source, &seenData}, func() error {
		c := AppChange{InstanceID: inst, RemoteID: remote, BindingID: binding, SourceID: source}
		switch state {
		case remoteChanged:
			c.State = ChangeUpdate
			// Dismiss takes the hash of the update the owner saw.
			hash, err := cardhash.ContentHash(seenData)
			if err != nil {
				return fmt.Errorf("binding %d seen copy: %w", binding, err)
			}
			c.AppHash = hash
		case remoteMissing:
			c.State = ChangeMissing
		default:
			c.State, c.Error = ChangeError, checkErr
		}
		add(book != 0, cmp.Or(card, book), c)
		return nil
	}); err != nil {
		return out, err
	}

	// An entry left out here is archived, so its row is dropped; its counts stay.
	var named []ChangeRow
	for _, k := range []*library.Kind{library.Card, library.Lorebook} {
		book := k == library.Lorebook
		var ids []int64
		for e := range entries {
			if e.book == book {
				ids = append(ids, e.id)
			}
		}
		nr, err := s.pool.Query(ctx, `SELECT e.id, coalesce(r.data->>'name', '') FROM `+k.Table+` e
			JOIN `+k.RevTable+` r ON r.id = e.head_revision_id WHERE e.id = ANY($1) AND e.archived_at IS NULL`, ids)
		if err != nil {
			return out, err
		}
		var id int64
		var name string
		if _, err := pgx.ForEachRow(nr, []any{&id, &name}, func() error {
			r := entries[key{book, id}]
			r.Name = name
			slices.SortFunc(r.Apps, func(a, b AppChange) int { return cmp.Compare(a.InstanceID, b.InstanceID) })
			named = append(named, *r)
			return nil
		}); err != nil {
			return out, err
		}
	}
	byName := func(a, b ChangeRow) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.CardID, b.CardID),
			cmp.Compare(a.LorebookID, b.LorebookID))
	}
	slices.SortFunc(orphans, byName)
	slices.SortFunc(named, byName)
	out.Rows = append(append(out.Rows, orphans...), named...)
	return out, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
