package library

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// EmbedText is a card's text to embed, from its head revision.
type EmbedText struct {
	CardID     int64
	RevisionID int64
	Text       string
	// EnrichmentID is the newest enrichment row Text came from, or nil when
	// it is the card's own fields.
	EnrichmentID *int64
}

// EmbedSource is an enrichment key whose value is embedded in place of a
// card's own fields.
type EmbedSource struct {
	Instance int64
	Key      string
}

// embedChars caps the text sent to a model; most embedding models read
// fewer than 8,192 tokens.
const embedChars = 8000

// Unembedded returns up to limit cards with an ID above after and text whose
// vector from instance is missing or stale, lowest ID first, in library or
// in every library for 0. The text is the sources' current values on the
// head revision, a list's items joined by commas; a card with none uses the
// fields that say who the character is, leaving out greetings and example
// dialogue. A vector goes stale when the head revision or its sources' values change.
func (l *Library) Unembedded(ctx context.Context, instance int64, sources []EmbedSource, library, after int64, limit int) ([]EmbedText, error) {
	instances, keys := make([]int64, len(sources)), make([]string, len(sources))
	for i, s := range sources {
		instances[i], keys[i] = s.Instance, s.Key
	}
	rows, err := l.pool.Query(ctx, `
		SELECT c.id, r.id, t.text, s.id
		FROM card c
		JOIN card_revision r ON r.id = c.head_revision_id
		CROSS JOIN LATERAL (
			SELECT string_agg(CASE jsonb_typeof(v.value)
					WHEN 'array' THEN (SELECT string_agg(x, ', ') FROM jsonb_array_elements_text(v.value) x)
					ELSE v.value #>> '{}' END, E'\n' ORDER BY v.instance_id, v.key) AS text,
				max(v.id) AS id
			FROM unnest($6::bigint[], $7::text[]) src (instance_id, key)
			CROSS JOIN LATERAL (SELECT id, instance_id, key, value FROM enrichment
				WHERE card_id = c.id AND revision_id = c.head_revision_id AND instance_id = src.instance_id AND key = src.key
				ORDER BY created_at DESC, id DESC LIMIT 1) v) s
		CROSS JOIN LATERAL (SELECT left(coalesce(nullif(btrim(s.text), ''), concat_ws(E'\n',
			nullif(btrim(r.data->>'name'), ''), nullif(btrim(r.data->>'description'), ''),
			nullif(btrim(r.data->>'personality'), ''), nullif(btrim(r.data->>'scenario'), ''),
			nullif(btrim(r.data->>'creator_notes'), ''))), $3) AS text) t
		LEFT JOIN card_embedding e ON e.instance_id = $1 AND e.card_id = c.id
		WHERE (e.revision_id IS DISTINCT FROM c.head_revision_id OR e.enrichment_id IS DISTINCT FROM s.id)
			AND t.text <> '' AND c.id > $4 AND ($5 = 0 OR c.library_id = $5)
		ORDER BY c.id
		LIMIT $2`, instance, limit, embedChars, after, library, instances, keys)
	if err != nil {
		return nil, fmt.Errorf("list unembedded cards for instance %d: %w", instance, err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[EmbedText])
	if err != nil {
		return nil, fmt.Errorf("read unembedded cards for instance %d: %w", instance, err)
	}
	return out, nil
}

// SaveEmbeddings stores vectors[i] as cards[i]'s vector from instance,
// replacing an older one.
func (l *Library) SaveEmbeddings(ctx context.Context, instance int64, cards []EmbedText, vectors [][]float32) error {
	if len(vectors) != len(cards) {
		return fmt.Errorf("save embeddings: %d vectors for %d cards", len(vectors), len(cards))
	}
	ids := make([]int64, len(cards))
	revs := make([]int64, len(cards))
	vecs := make([]string, len(cards))
	enrichments := make([]*int64, len(cards))
	for i, c := range cards {
		ids[i], revs[i], vecs[i], enrichments[i] = c.CardID, c.RevisionID, vectorText(vectors[i]), c.EnrichmentID
	}
	_, err := l.pool.Exec(ctx, `
		INSERT INTO card_embedding (instance_id, card_id, revision_id, embedding, enrichment_id)
		SELECT $1, u.card_id, u.revision_id, u.embedding::vector, u.enrichment_id
		FROM unnest($2::bigint[], $3::bigint[], $4::text[], $5::bigint[]) AS u (card_id, revision_id, embedding, enrichment_id)
		ON CONFLICT (instance_id, card_id) DO UPDATE
			SET revision_id = EXCLUDED.revision_id, embedding = EXCLUDED.embedding, enrichment_id = EXCLUDED.enrichment_id`,
		instance, ids, revs, vecs, enrichments)
	if err != nil {
		return fmt.Errorf("save embeddings for instance %d: %w", instance, err)
	}
	return nil
}

// ClearEmbeddings drops every vector, so every card is embedded again.
func (l *Library) ClearEmbeddings(ctx context.Context) error {
	if _, err := l.pool.Exec(ctx, `DELETE FROM card_embedding`); err != nil {
		return fmt.Errorf("clear embeddings: %w", err)
	}
	return nil
}

// vectorText writes v in pgvector's text form, so no pgvector Go package is needed.
func vectorText(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
