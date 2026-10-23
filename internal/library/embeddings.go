package library

import (
	"context"
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

// Unembedded returns up to limit cards with text whose head revision has no
// vector from instance, lowest ID first. The text is the fields that say
// who the character is, leaving out greetings and example dialogue.
func (l *Library) Unembedded(ctx context.Context, instance int64, limit int) ([]EmbedText, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT c.id, r.id, left(concat_ws(E'\n', nullif(r.data->>'name', ''), nullif(r.data->>'description', ''),
			nullif(r.data->>'personality', ''), nullif(r.data->>'scenario', ''), nullif(r.data->>'creator_notes', '')), $3) AS text
		FROM card c
		JOIN card_revision r ON r.id = c.head_revision_id
		LEFT JOIN card_embedding e ON e.instance_id = $1 AND e.card_id = c.id
		WHERE e.revision_id IS DISTINCT FROM c.head_revision_id
			AND concat(r.data->>'name', r.data->>'description', r.data->>'personality', r.data->>'scenario', r.data->>'creator_notes') <> ''
		ORDER BY c.id
		LIMIT $2`, instance, limit, embedChars)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[EmbedText])
}

// SaveEmbeddings stores vectors[i] as cards[i]'s vector from instance,
// replacing an older one.
func (l *Library) SaveEmbeddings(ctx context.Context, instance int64, cards []EmbedText, vectors [][]float32) error {
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
	return err
}

// DeleteEmbeddings drops instance's vectors, so every card is embedded again.
func (l *Library) DeleteEmbeddings(ctx context.Context, instance int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM card_embedding WHERE instance_id = $1`, instance)
	return err
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
