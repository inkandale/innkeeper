package library

import (
	"context"
	"encoding/json"
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
	cards, revs, keys, values, metas := make([]int64, len(vals)), make([]int64, len(vals)),
		make([]string, len(vals)), make([]string, len(vals)), make([]string, len(vals))
	for i, v := range vals {
		cards[i], revs[i], keys[i], values[i], metas[i] = v.CardID, v.RevisionID, v.Key, string(v.Value), string(v.Meta)
		if len(v.Meta) == 0 {
			metas[i] = "{}"
		}
	}
	_, err := l.pool.Exec(ctx, `INSERT INTO enrichment (card_id, revision_id, instance_id, key, value, meta)
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
		SELECT e.*, row_number() OVER (PARTITION BY e.instance_id, e.key ORDER BY e.created_at DESC, e.id DESC) = 1 AS current,
			e.revision_id <> c.head_revision_id AS stale
		FROM enrichment e JOIN card c ON c.id = e.card_id WHERE e.card_id = $1) x
		WHERE current OR $2 ORDER BY instance_id, key, created_at DESC, id DESC`, cardID, history)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Enrichment])
}
