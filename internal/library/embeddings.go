package library

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/inkandale/innkeeper/internal/db"
)

// NewVector is one card's vector from an enrich instance, from the
// revision named.
type NewVector struct {
	CardID     int64
	RevisionID int64
	Space      string
	Vector     []float32
}

// SaveVectors stores each live card's vector from instance, scored under its
// plugin's enrich_version, replacing the one it had. It skips an archived
// card, which may have been archived mid-embed.
func (l *Library) SaveVectors(ctx context.Context, instance int64, version int, vals []NewVector) error {
	ids, revs, spaces, vecs := make([]int64, len(vals)), make([]int64, len(vals)), make([]string, len(vals)), make([]string, len(vals))
	for i, v := range vals {
		ids[i], revs[i], spaces[i], vecs[i] = v.CardID, v.RevisionID, v.Space, vectorText(v.Vector)
	}
	_, err := l.pool.Exec(ctx, `
		INSERT INTO card_embedding (instance_id, card_id, revision_id, space, embedding, enrich_version)
		SELECT $1, v.card_id, v.revision_id, v.space, v.embedding::vector, $6
		FROM unnest($2::bigint[], $3::bigint[], $4::text[], $5::text[]) v (card_id, revision_id, space, embedding)
		JOIN card c ON c.id = v.card_id AND c.archived_at IS NULL
		ON CONFLICT (instance_id, card_id) DO UPDATE
		SET revision_id = excluded.revision_id, space = excluded.space, embedding = excluded.embedding,
			enrich_version = excluded.enrich_version`,
		instance, ids, revs, spaces, vecs, version)
	if err != nil {
		return fmt.Errorf("save %d vectors for instance %d: %w", len(vals), instance, err)
	}
	return nil
}

// HasVectors reports whether this install stores vectors. A successful read
// is kept: enabling pgvector takes a restart. An error is not, so a cancelled
// first call doesn't fail every later one.
func (l *Library) HasVectors(ctx context.Context) (bool, error) {
	l.vectorsMu.Lock()
	defer l.vectorsMu.Unlock()
	if l.vectorsDone {
		return l.vectors, nil
	}
	ok, err := db.VectorsAvailable(ctx, l.pool)
	if err != nil {
		return false, err
	}
	l.vectors, l.vectorsDone = ok, true
	return ok, nil
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

// DeleteVectorsExcept deletes every vector not from instance.
func (l *Library) DeleteVectorsExcept(ctx context.Context, instance int64) error {
	if ok, err := l.HasVectors(ctx); err != nil || !ok {
		return err
	}
	if _, err := l.pool.Exec(ctx, `DELETE FROM card_embedding WHERE instance_id <> $1`, instance); err != nil {
		return fmt.Errorf("delete vectors not from instance %d: %w", instance, err)
	}
	return nil
}
