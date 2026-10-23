package library

import (
	"cmp"
	"context"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Why the hide job archived a card.
const (
	ReasonLowScore  = "low_score"
	ReasonDuplicate = "duplicate"
)

// hideable is a card the hide job may group or archive: not archived by the owner.
const hideable = `(c.archived_at IS NULL OR c.archived_reason IS NOT NULL)`

// SetHidden makes hidden, card ID to reason, the hide job's archived cards in
// library: it archives those not archived yet, updates the reason of those it
// already hid, and un-archives the ones it hid before that aren't in hidden.
// The owner's archived cards stay as they are. It returns how many cards it
// archived and un-archived.
func (l *Library) SetHidden(ctx context.Context, library int64, hidden map[int64]string) (int, int, error) {
	ids := slices.Sorted(maps.Keys(hidden))
	reasons := make([]string, len(ids))
	for i, id := range ids {
		reasons[i] = hidden[id]
	}
	var archived, unarchived int
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE card SET archived_at = NULL, archived_reason = NULL
			WHERE library_id = $1 AND archived_reason IS NOT NULL AND NOT (id = ANY($2))`, library, ids)
		if err != nil {
			return err
		}
		unarchived = int(tag.RowsAffected())
		if _, err := tx.Exec(ctx, `UPDATE card c SET archived_reason = h.reason
			FROM unnest($2::bigint[], $3::text[]) h(id, reason)
			WHERE c.id = h.id AND c.library_id = $1 AND c.archived_reason IS NOT NULL AND c.archived_reason <> h.reason`,
			library, ids, reasons); err != nil {
			return err
		}
		tag, err = tx.Exec(ctx, `UPDATE card c SET archived_at = now(), archived_reason = h.reason
			FROM unnest($2::bigint[], $3::text[]) h(id, reason)
			WHERE c.id = h.id AND c.library_id = $1 AND c.archived_at IS NULL`, library, ids, reasons)
		archived = int(tag.RowsAffected())
		return err
	})
	return archived, unarchived, err
}

// DuplicatePair is two cards of one library that look like the same card:
// Exact when their head revisions have the same content, and their text
// within the asked distance otherwise. A is the lower ID.
type DuplicatePair struct {
	A, B  int64
	Exact bool
}

// band16Distance is the widest distance hash_bands16's 4 bands always catch.
const band16Distance = 3

// DuplicatePairs returns library's pairs of cards whose head revisions have
// the same content or, unless maxDistance is negative, text or the avatar
// and name within maxDistance bits, in (A, B) order. Cards the owner
// archived are in no pair. An image alone doesn't make a duplicate: archives
// reuse art for different characters.
func (l *Library) DuplicatePairs(ctx context.Context, library int64, maxDistance int) ([]DuplicatePair, error) {
	if maxDistance > NearDistance {
		return nil, errWideDistance
	}
	pairs := `SELECT c.id, e.id, true FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		JOIN card_revision r2 ON r2.content_hash = r.content_hash
		JOIN card e ON e.id = r2.card_id AND e.head_revision_id = r2.id AND e.library_id = c.library_id AND e.id > c.id
		WHERE c.library_id = $1 AND ` + hideable + ` AND (e.archived_at IS NULL OR e.archived_reason IS NOT NULL)`
	args := []any{library}
	if maxDistance >= 0 {
		args = append(args, maxDistance)
		bands := `r2.text_bands && hash_bands(r.text_simhash)`
		if maxDistance <= band16Distance {
			bands = `r2.text_bands16 && hash_bands16(r.text_simhash)`
		}
		// Matching names first keeps a widely reused avatar from pairing every card that has it.
		pairs = `WITH named AS (
			SELECT c.id, lower(btrim(r.data->>'name')) AS name, b.avatar_phash AS phash
			FROM card c JOIN card_revision r ON r.id = c.head_revision_id JOIN blob b ON b.sha256 = r.avatar_blob
			WHERE c.library_id = $1 AND ` + hideable + ` AND b.avatar_phash IS NOT NULL AND btrim(r.data->>'name') <> ''
		)
		` + pairs + `
		UNION ALL
		SELECT c.id, m.id, false FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		CROSS JOIN LATERAL (
			SELECT e.id FROM card_revision r2
			JOIN card e ON e.id = r2.card_id AND e.head_revision_id = r2.id
			WHERE e.library_id = c.library_id AND e.id > c.id AND (e.archived_at IS NULL OR e.archived_reason IS NOT NULL)
				AND ` + bands + `
				AND bit_count((r2.text_simhash # r.text_simhash)::bit(64)) <= $2
		) m
		WHERE c.library_id = $1 AND ` + hideable + ` AND r.text_simhash IS NOT NULL
		UNION ALL
		SELECT a.id, b.id, false FROM named a JOIN named b ON b.name = a.name AND b.id > a.id
		WHERE bit_count((a.phash # b.phash)::bit(64)) <= $2`
	}
	rows, err := l.pool.Query(ctx, pairs, args...)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[DuplicatePair])
	if err != nil {
		return nil, err
	}
	// A pair matching by content and by text is exact.
	slices.SortFunc(found, func(x, y DuplicatePair) int {
		return cmp.Or(cmp.Compare(x.A, y.A), cmp.Compare(x.B, y.B), compareBool(y.Exact, x.Exact))
	})
	return slices.CompactFunc(found, func(x, y DuplicatePair) bool { return x.A == y.A && x.B == y.B }), nil
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// GroupPairs joins pairs that share a card into groups, each in ID order,
// the groups ordered by their first card.
func GroupPairs(pairs []DuplicatePair) [][]int64 {
	parent := map[int64]int64{}
	var find func(int64) int64
	find = func(x int64) int64 {
		p, ok := parent[x]
		if !ok || p == x {
			parent[x] = x
			return x
		}
		root := find(p)
		parent[x] = root
		return root
	}
	for _, p := range pairs {
		ra, rb := find(p.A), find(p.B)
		parent[max(ra, rb)] = min(ra, rb)
	}
	members := map[int64][]int64{}
	for id := range parent {
		root := find(id)
		members[root] = append(members[root], id)
	}
	groups := make([][]int64, 0, len(members))
	for _, g := range members {
		slices.Sort(g)
		groups = append(groups, g)
	}
	slices.SortFunc(groups, func(x, y []int64) int { return cmp.Compare(x[0], y[0]) })
	return groups
}

// HideCandidate is a card the hide job weighs: one the owner hasn't archived.
type HideCandidate struct {
	ID        int64
	CreatedAt time.Time
	// Score is the score instance's current value of the key, if it has one.
	Score *float64
}

// HideCandidates returns library's cards the hide job may archive, in ID
// order, with their current value of key from the enrich instance.
func (l *Library) HideCandidates(ctx context.Context, library, instance int64, key string) ([]HideCandidate, error) {
	rows, err := l.pool.Query(ctx, `SELECT c.id, c.created_at,
			(SELECT CASE WHEN jsonb_typeof(e.value) = 'number' THEN e.value::float8 END FROM enrichment e
			 WHERE e.card_id = c.id AND e.key = $3 AND e.instance_id = $2
			 ORDER BY e.revision_id DESC, e.created_at DESC, e.id DESC LIMIT 1)
		FROM card c WHERE c.library_id = $1 AND `+hideable+` ORDER BY c.id`, library, instance, key)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[HideCandidate])
}

// PlanHide is what the hide job archives, card ID to reason: every candidate
// scoring below threshold, if set, and in each duplicate group every card
// but the one keep picks: best (highest score, then oldest), oldest, or
// newest. A card both low and a duplicate is low.
func PlanHide(cands []HideCandidate, groups [][]int64, threshold *float64, keep string) map[int64]string {
	byID := map[int64]HideCandidate{}
	hidden := map[int64]string{}
	for _, c := range cands {
		byID[c.ID] = c
		if threshold != nil && c.Score != nil && *c.Score < *threshold {
			hidden[c.ID] = ReasonLowScore
		}
	}
	better := func(a, b HideCandidate) bool {
		switch keep {
		case "newest":
			return a.CreatedAt.After(b.CreatedAt) || a.CreatedAt.Equal(b.CreatedAt) && a.ID > b.ID
		case "oldest":
		default:
			sa, sb := cmp.Or(a.Score, &negInf), cmp.Or(b.Score, &negInf)
			if *sa != *sb {
				return *sa > *sb
			}
		}
		return a.CreatedAt.Before(b.CreatedAt) || a.CreatedAt.Equal(b.CreatedAt) && a.ID < b.ID
	}
	for _, g := range groups {
		var kept HideCandidate
		for i, id := range g {
			if c := byID[id]; i == 0 || better(c, kept) {
				kept = c
			}
		}
		for _, id := range g {
			if _, low := hidden[id]; id != kept.ID && !low {
				hidden[id] = ReasonDuplicate
			}
		}
	}
	return hidden
}

var negInf = math.Inf(-1)
