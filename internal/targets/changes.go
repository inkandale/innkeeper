package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/cardhash"
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
		names[in.ID] = in.Name
	}
	cards := map[int64]*ChangeRow{}
	var orphans []ChangeRow
	seen := map[[2]int64]bool{} // card and instance with a snapshot change
	// count tallies c and says whether the filter keeps it.
	count := func(c AppChange) bool {
		if out.Counts[c.InstanceID] == nil {
			out.Counts[c.InstanceID] = map[string]int{}
		}
		out.Counts[c.InstanceID][c.State]++
		return (f.InstanceID == 0 || c.InstanceID == f.InstanceID) && (f.State == "" || c.State == f.State)
	}
	add := func(card int64, c AppChange) {
		kept := count(c)
		if card != 0 {
			seen[[2]int64{card, c.InstanceID}] = true
		}
		if !kept {
			return
		}
		if card == 0 {
			orphans = append(orphans, ChangeRow{Name: fmt.Sprintf("%s (%s only)", c.Name, names[c.InstanceID]), Apps: []AppChange{c}})
			return
		}
		r := cards[card]
		if r == nil {
			r = &ChangeRow{CardID: card}
			cards[card] = r
		}
		r.Apps = append(r.Apps, c)
	}
	books := map[int64]*ChangeRow{}
	addBook := func(lorebook int64, c AppChange) {
		if !count(c) {
			return
		}
		r := books[lorebook]
		if r == nil {
			r = &ChangeRow{LorebookID: lorebook}
			books[lorebook] = r
		}
		r.Apps = append(r.Apps, c)
	}

	// Each live binding's card and accepted copy, by instance and remote ID, to
	// drop entries settled since the snapshot; and the cards bound now.
	type live struct {
		card int64
		hash string
	}
	accepted := map[int64]map[string]live{}
	boundNow := map[[2]int64]bool{} // card and instance
	rows, err := s.pool.Query(ctx, `SELECT instance_id, remote_id, card_id, accepted_hash FROM binding
		WHERE card_id IS NOT NULL AND push AND remote_state <> 'missing'`)
	if err != nil {
		return out, err
	}
	var inst, bcard int64
	var remote, hash string
	if _, err := pgx.ForEachRow(rows, []any{&inst, &remote, &bcard, &hash}, func() error {
		if accepted[inst] == nil {
			accepted[inst] = map[string]live{}
		}
		accepted[inst][remote] = live{bcard, hash}
		boundNow[[2]int64{bcard, inst}] = true
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
	for _, l := range latest {
		for _, it := range l.snap.Items {
			c := AppChange{InstanceID: l.inst, RemoteID: it.RemoteID, Name: it.Name, BindingID: it.BindingID, Push: true,
				JobID: l.job, AppHash: it.AppHash, Suggested: it.Suggested, Error: it.Error}
			acc, isBound := accepted[l.inst][it.RemoteID]
			card := int64(0)
			switch {
			case it.AutoBound || it.AutoImported || it.AutoPulled:
				continue
			case it.Verdict == VerdictError:
				// An item the app can't read has no candidates; a bound one belongs to its binding's card.
				c.State = ChangeError
				if isBound {
					card = acc.card
				} else if len(it.Candidates) > 0 {
					card = it.Candidates[0].CardID
				}
			case it.Verdict == VerdictBound:
				// A library-only change shows as ahead from the binding status instead.
				if !it.AppChanged || it.AppOnly || len(it.Candidates) == 0 || (isBound && acc.hash == it.AppHash) {
					continue
				}
				c.State, card = ChangeBothChanged, it.Candidates[0].CardID
			case isBound:
				// Bound since the snapshot.
				continue
			default:
				// A card bound on this app since the snapshot, as a duplicate's sibling may be, is taken.
				i := slices.IndexFunc(it.Candidates, func(c Candidate) bool {
					return c.BoundTo == "" && !boundNow[[2]int64{c.CardID, l.inst}]
				})
				if it.Verdict == VerdictNew || i < 0 {
					c.State = ChangeNew
				} else {
					c.State, card = ChangeDiffers, it.Candidates[i].CardID
				}
			}
			add(card, c)
		}
	}

	st, err := s.pool.Query(ctx, `SELECT b.card_id, b.instance_id, b.id, b.remote_id, s.status FROM binding b
		JOIN binding_status s ON s.binding_id = b.id JOIN card c ON c.id = b.card_id
		WHERE c.archived_at IS NULL AND b.push AND s.status IN ('ahead', 'missing') ORDER BY b.id`)
	if err != nil {
		return out, err
	}
	var card, binding int64
	var status string
	if _, err := pgx.ForEachRow(st, []any{&card, &inst, &binding, &remote, &status}, func() error {
		if !seen[[2]int64{card, inst}] {
			add(card, AppChange{InstanceID: inst, State: status, RemoteID: remote, BindingID: binding, Push: true})
		}
		return nil
	}); err != nil {
		return out, err
	}

	// Followed sources, from their last check. One the owner removed has no
	// live source row, so it is gone from the queue before the next check unbinds it.
	fl, err := s.pool.Query(ctx, `SELECT coalesce(b.card_id, 0), coalesce(b.lorebook_id, 0), b.instance_id, b.id, b.remote_id,
			b.remote_state, coalesce(b.check_error, ''), src.id, b.seen_data
		FROM binding b LEFT JOIN card c ON c.id = b.card_id LEFT JOIN lorebook l ON l.id = b.lorebook_id
		JOIN LATERAL (SELECT s.id FROM card_source s WHERE s.card_id = b.card_id AND s.removed_at IS NULL
				AND s.kind || ':' || s.ref = b.remote_id
			UNION ALL SELECT s.id FROM lorebook_source s WHERE s.lorebook_id = b.lorebook_id AND s.removed_at IS NULL
				AND s.kind || ':' || s.ref = b.remote_id
			LIMIT 1) src ON TRUE
		WHERE NOT b.push AND NOT b.paused AND coalesce(c.archived_at, l.archived_at) IS NULL
			AND (b.remote_state IN ('changed', 'missing') OR b.check_failures >= $1)
		ORDER BY b.id`, failuresShown)
	if err != nil {
		return out, err
	}
	var book, source int64
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
		if book != 0 {
			addBook(book, c)
		} else {
			add(card, c)
		}
		return nil
	}); err != nil {
		return out, err
	}

	ids := make([]int64, 0, len(cards))
	for id := range cards {
		ids = append(ids, id)
	}
	// A card left out here is archived, so its row is dropped; its counts stay.
	nr, err := s.pool.Query(ctx, `SELECT c.id, coalesce(r.data->>'name', '') FROM card c
		JOIN card_revision r ON r.id = c.head_revision_id WHERE c.id = ANY($1) AND c.archived_at IS NULL`, ids)
	if err != nil {
		return out, err
	}
	var id int64
	var name string
	var named []ChangeRow
	if _, err := pgx.ForEachRow(nr, []any{&id, &name}, func() error {
		r := cards[id]
		r.Name = name
		slices.SortFunc(r.Apps, func(a, b AppChange) int { return cmp.Compare(a.InstanceID, b.InstanceID) })
		named = append(named, *r)
		return nil
	}); err != nil {
		return out, err
	}
	lr, err := s.pool.Query(ctx, `SELECT l.id, coalesce(r.data->>'name', '') FROM lorebook l
		JOIN lorebook_revision r ON r.id = l.head_revision_id WHERE l.id = ANY($1)`, slices.Collect(maps.Keys(books)))
	if err != nil {
		return out, err
	}
	if _, err := pgx.ForEachRow(lr, []any{&id, &name}, func() error {
		r := books[id]
		r.Name = name
		named = append(named, *r)
		return nil
	}); err != nil {
		return out, err
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
