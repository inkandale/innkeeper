package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Changes lists every card some app needs the owner for, and the app items no
// card matches, from each app's latest finished reconcile and the binding
// statuses. An entry the owner settled after that reconcile is left out.
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
	add := func(card int64, c AppChange) {
		if out.Counts[c.InstanceID] == nil {
			out.Counts[c.InstanceID] = map[string]int{}
		}
		out.Counts[c.InstanceID][c.State]++
		if card != 0 {
			seen[[2]int64{card, c.InstanceID}] = true
		}
		if (f.InstanceID != 0 && c.InstanceID != f.InstanceID) || (f.State != "" && c.State != f.State) {
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

	// The copy each live binding accepted, by instance and remote ID, to drop
	// entries settled since the snapshot.
	accepted := map[int64]map[string]string{}
	rows, err := s.pool.Query(ctx, `SELECT instance_id, remote_id, accepted_hash FROM binding
		WHERE card_id IS NOT NULL AND remote_state <> 'missing'`)
	if err != nil {
		return out, err
	}
	var inst int64
	var remote, hash string
	if _, err := pgx.ForEachRow(rows, []any{&inst, &remote, &hash}, func() error {
		if accepted[inst] == nil {
			accepted[inst] = map[string]string{}
		}
		accepted[inst][remote] = hash
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
			c := AppChange{InstanceID: l.inst, RemoteID: it.RemoteID, Name: it.Name, BindingID: it.BindingID,
				JobID: l.job, AppHash: it.AppHash, Suggested: it.Suggested, Error: it.Error}
			acc, isBound := accepted[l.inst][it.RemoteID]
			card := int64(0)
			switch {
			case it.AutoBound || it.AutoImported || it.AutoPulled:
				continue
			case it.Verdict == VerdictBound:
				// A library-only change shows as ahead from the binding status instead.
				if !it.AppChanged || it.AppOnly || len(it.Candidates) == 0 || (isBound && acc == it.AppHash) {
					continue
				}
				c.State, card = ChangeBothChanged, it.Candidates[0].CardID
			case isBound:
				// Bound since the snapshot.
				continue
			case it.Verdict == VerdictError:
				c.State = ChangeError
				if len(it.Candidates) > 0 {
					card = it.Candidates[0].CardID
				}
			default:
				i := unbound(it.Candidates)
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
		WHERE c.archived_at IS NULL AND s.status IN ('ahead', 'missing') ORDER BY b.id`)
	if err != nil {
		return out, err
	}
	var card, binding int64
	var status string
	if _, err := pgx.ForEachRow(st, []any{&card, &inst, &binding, &remote, &status}, func() error {
		if !seen[[2]int64{card, inst}] {
			add(card, AppChange{InstanceID: inst, State: status, RemoteID: remote, BindingID: binding})
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
	byName := func(a, b ChangeRow) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.CardID, b.CardID))
	}
	slices.SortFunc(orphans, byName)
	slices.SortFunc(named, byName)
	out.Rows = append(append(out.Rows, orphans...), named...)
	return out, nil
}
