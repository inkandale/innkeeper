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
}

// embedChars caps the text sent to a model; most embedding models read
// fewer than 8,192 tokens.
const embedChars = 8000

// Unembedded returns up to limit cards with an ID above after and text whose
// head revision has no vector from instance, lowest ID first. The text is the
// fields that say who the character is, leaving out greetings and example
// dialogue.
func (l *Library) Unembedded(ctx context.Context, instance, after int64, limit int) ([]EmbedText, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT c.id, r.id, t.text
		FROM card c
		JOIN card_revision r ON r.id = c.head_revision_id
		CROSS JOIN LATERAL (SELECT left(concat_ws(E'\n',
			nullif(btrim(r.data->>'name'), ''), nullif(btrim(r.data->>'description'), ''),
			nullif(btrim(r.data->>'personality'), ''), nullif(btrim(r.data->>'scenario'), ''),
			nullif(btrim(r.data->>'creator_notes'), '')), $3) AS text) t
		LEFT JOIN card_embedding e ON e.instance_id = $1 AND e.card_id = c.id
		WHERE e.revision_id IS DISTINCT FROM c.head_revision_id AND t.text <> '' AND c.id > $4
		ORDER BY c.id
		LIMIT $2`, instance, limit, embedChars, after)
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
	for i, c := range cards {
		ids[i], revs[i], vecs[i] = c.CardID, c.RevisionID, vectorText(vectors[i])
	}
	_, err := l.pool.Exec(ctx, `
		INSERT INTO card_embedding (instance_id, card_id, revision_id, embedding)
		SELECT $1, u.card_id, u.revision_id, u.embedding::vector
		FROM unnest($2::bigint[], $3::bigint[], $4::text[]) AS u (card_id, revision_id, embedding)
		ON CONFLICT (instance_id, card_id) DO UPDATE SET revision_id = EXCLUDED.revision_id, embedding = EXCLUDED.embedding`,
		instance, ids, revs, vecs)
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
