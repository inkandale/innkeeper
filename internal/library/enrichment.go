package library

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NewEnrichment is one value an enrich plugin computed for a card revision.
type NewEnrichment struct {
	CardID     int64
	RevisionID int64
	Key        string
	Value      json.RawMessage
	// Meta is an object, or empty for none.
	Meta json.RawMessage
}

// Enrichment is one stored enrichment value.
type Enrichment struct {
	InstanceID int64           `json:"instance_id"`
	RevisionID int64           `json:"revision_id"`
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
	Meta       json.RawMessage `json:"meta"`
	CreatedAt  time.Time       `json:"created_at"`
	Current    bool            `json:"current" doc:"Whether this is the newest value of its key from its instance."`
	Stale      bool            `json:"stale" doc:"Whether the value is about a revision that is no longer the card's head."`
}

// AddEnrichments stores values from instanceID. A value whose revision isn't
// one of its card's is left out.
func (l *Library) AddEnrichments(ctx context.Context, instanceID int64, vals []NewEnrichment) error {
	return addEnrichments(ctx, l.pool, instanceID, vals)
}

func addEnrichments(ctx context.Context, q execer, instanceID int64, vals []NewEnrichment) error {
	cards, revs, keys, values, metas := make([]int64, len(vals)), make([]int64, len(vals)),
		make([]string, len(vals)), make([]string, len(vals)), make([]string, len(vals))
	for i, v := range vals {
		cards[i], revs[i], keys[i], values[i], metas[i] = v.CardID, v.RevisionID, v.Key, string(v.Value), string(v.Meta)
		if len(v.Meta) == 0 {
			metas[i] = "{}"
		}
	}
	_, err := q.Exec(ctx, `INSERT INTO enrichment (card_id, revision_id, instance_id, key, value, meta)
		SELECT v.card_id, v.revision_id, $1, v.key, v.value::jsonb, v.meta::jsonb
		FROM unnest($2::bigint[], $3::bigint[], $4::text[], $5::text[], $6::text[]) WITH ORDINALITY v(card_id, revision_id, key, value, meta, ord)
		JOIN card_revision r ON r.id = v.revision_id AND r.card_id = v.card_id
		ORDER BY v.ord`, instanceID, cards, revs, keys, values, metas)
	return err
}

// Enrichments returns a card's current values from every instance, by
// instance and key. With history it adds the older values, each after the
// value that replaced it.
func (l *Library) Enrichments(ctx context.Context, cardID int64, history bool) ([]Enrichment, error) {
	rows, err := l.pool.Query(ctx, `SELECT instance_id, revision_id, key, value, meta, created_at, current, stale FROM (
		SELECT e.*, row_number() OVER (PARTITION BY e.instance_id, e.key ORDER BY e.revision_id DESC, e.created_at DESC, e.id DESC) = 1 AS current,
			e.revision_id <> c.head_revision_id AS stale
		FROM enrichment e JOIN card c ON c.id = e.card_id WHERE e.card_id = $1) x
		WHERE current OR $2 ORDER BY instance_id, key, revision_id DESC, created_at DESC, id DESC`, cardID, history)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Enrichment])
}

// HasEnrichments reports whether instanceID has stored a value for any card.
func (l *Library) HasEnrichments(ctx context.Context, instanceID int64) (bool, error) {
	var has bool
	err := l.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM enrichment WHERE instance_id = $1)`, instanceID).Scan(&has)
	return has, err
}

// EnrichedRevisions returns which of revIDs hold a value or a vector from
// instanceID.
func (l *Library) EnrichedRevisions(ctx context.Context, instanceID int64, cardIDs, revIDs []int64) ([]int64, error) {
	vectors, err := l.HasVectors(ctx)
	if err != nil {
		return nil, err
	}
	// card_id leads the index; revision_id alone would scan the table.
	sql := `SELECT revision_id FROM enrichment WHERE card_id = ANY($2) AND instance_id = $1 AND revision_id = ANY($3)`
	if vectors {
		sql += ` UNION SELECT revision_id FROM card_embedding WHERE card_id = ANY($2) AND instance_id = $1 AND revision_id = ANY($3)`
	}
	rows, err := l.pool.Query(ctx, sql, instanceID, cardIDs, revIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// RevisionRef names one revision of one card.
type RevisionRef struct {
	CardID     int64
	RevisionID int64
}

// RecordEnrichFailures records that instance's plugin failed on revs, so
// the sweep doesn't send them again.
func (l *Library) RecordEnrichFailures(ctx context.Context, instance int64, revs []RevisionRef) error {
	cards, ids := make([]int64, len(revs)), make([]int64, len(revs))
	for i, r := range revs {
		cards[i], ids[i] = r.CardID, r.RevisionID
	}
	_, err := l.pool.Exec(ctx, `INSERT INTO enrich_failure (instance_id, card_id, revision_id)
		SELECT $1, c, r FROM unnest($2::bigint[], $3::bigint[]) x (c, r)
		ON CONFLICT (instance_id, revision_id) DO UPDATE SET failed_at = now()`, instance, cards, ids)
	if err != nil {
		return fmt.Errorf("record %d enrich failures for instance %d: %w", len(revs), instance, err)
	}
	return nil
}

// ChangedUnenriched returns up to limit live cards changed after since
// whose head holds no value or vector from instance and hasn't failed on
// it, lowest ID first.
func (l *Library) ChangedUnenriched(ctx context.Context, instance int64, since time.Time, limit int) ([]int64, error) {
	vectors, err := l.HasVectors(ctx)
	if err != nil {
		return nil, err
	}
	noVector := ""
	if vectors {
		noVector = ` AND NOT EXISTS (SELECT 1 FROM card_embedding v
			WHERE v.card_id = c.id AND v.instance_id = $1 AND v.revision_id = c.head_revision_id)`
	}
	rows, err := l.pool.Query(ctx, `SELECT c.id FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE c.archived_at IS NULL AND greatest(r.created_at, c.restored_at) > $2
			AND NOT EXISTS (SELECT 1 FROM enrichment e WHERE e.card_id = c.id AND e.instance_id = $1 AND e.revision_id = c.head_revision_id)
			AND NOT EXISTS (SELECT 1 FROM enrich_failure f WHERE f.instance_id = $1 AND f.revision_id = c.head_revision_id)`+noVector+`
		ORDER BY c.id LIMIT $3`, instance, since, limit)
	if err != nil {
		return nil, fmt.Errorf("list changed cards for instance %d: %w", instance, err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
