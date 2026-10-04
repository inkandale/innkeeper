package library

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Match is another card that looks like a duplicate.
type Match struct {
	CardID int64
	// Reason is content (same content_hash), text (text_simhash), or avatar
	// (avatar_phash).
	Reason string
	// Distance is the Hamming distance between hashes; 0 for content.
	Distance int
}

// SimilarCards compares a card's head revision with every other card's head
// revision and returns matches within maxDistance bits, closest first. The
// spec allows a pairwise scan at library scale.
func (l *Library) SimilarCards(ctx context.Context, cardID int64, maxDistance int) ([]Match, error) {
	rows, err := l.pool.Query(ctx, `
		WITH heads AS (
			SELECT c.id, r.content_hash, r.text_simhash, b.avatar_phash
			FROM card c
			JOIN card_revision r ON r.id = c.head_revision_id
			LEFT JOIN blob b ON b.sha256 = r.avatar_blob
		),
		me AS (SELECT * FROM heads WHERE id = $1),
		other AS (SELECT * FROM heads WHERE id <> $1)
		SELECT * FROM (
			SELECT o.id, 'content', 0 FROM other o, me WHERE o.content_hash = me.content_hash
			UNION ALL
			SELECT o.id, 'text', bit_count((o.text_simhash # me.text_simhash)::bit(64))::int
			FROM other o, me WHERE bit_count((o.text_simhash # me.text_simhash)::bit(64)) <= $2
			UNION ALL
			SELECT o.id, 'avatar', bit_count((o.avatar_phash # me.avatar_phash)::bit(64))::int
			FROM other o, me WHERE bit_count((o.avatar_phash # me.avatar_phash)::bit(64)) <= $2
		) m (card_id, reason, distance)
		ORDER BY distance, card_id, reason`, cardID, maxDistance)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Match])
}
