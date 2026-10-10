package library

import (
	"cmp"
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Match is another card that looks like a duplicate.
type Match struct {
	CardID int64 `json:"card_id"`
	// Reason is content (same content_hash), text (text_simhash), or avatar
	// (avatar_phash).
	Reason string `json:"reason"`
	// Distance is the Hamming distance between hashes; 0 for content.
	Distance int `json:"distance"`
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

// NearDistance is the most differing hash bits that count as a near match.
// GET /cards/{id}/similar defaults to the same.
const NearDistance = 8

// Hashes are an outside item's fingerprints, as cardhash computes them.
type Hashes struct {
	ContentHash string
	TextSimhash *int64
	ImagePhash  *int64
}

// HashMatch is a library card or lorebook that matches an outside item.
type HashMatch struct {
	ID       int64  `json:"id"`
	Reason   string `json:"reason" enum:"content,text,avatar"`
	Distance int    `json:"distance"`
}

// MatchHashes returns, for each of hashes, the entities whose head revision
// has the same content_hash, or a text_simhash or avatar phash within
// maxDistance bits, closest first. An entity that matches more than one way
// is listed once, by its closest reason.
//
// ponytail: scans every head revision for every item, as SimilarCards does;
// index the hashes if libraries outgrow a scan.
func (s *Entities) MatchHashes(ctx context.Context, hashes []Hashes, maxDistance int) ([][]HashMatch, error) {
	out := make([][]HashMatch, len(hashes))
	if len(hashes) == 0 {
		return out, nil
	}
	content := make([]string, len(hashes))
	text := make([]*int64, len(hashes))
	image := make([]*int64, len(hashes))
	for i, h := range hashes {
		content[i], text[i], image[i] = h.ContentHash, h.TextSimhash, h.ImagePhash
	}
	rows, err := s.pool.Query(ctx, `
		WITH q AS (
			SELECT * FROM unnest($1::text[], $2::bigint[], $3::bigint[])
				WITH ORDINALITY AS q (content_hash, text_simhash, image_phash, i)
		),
		heads AS (
			SELECT e.id, r.content_hash, r.text_simhash, b.avatar_phash
			FROM `+s.k.table+` e
			JOIN `+s.k.revTable+` r ON r.id = e.head_revision_id
			LEFT JOIN blob b ON b.sha256 = r.avatar_blob
		)
		SELECT DISTINCT ON (i, id) i::int, id, reason, distance FROM (
			SELECT q.i, h.id, 'content' AS reason, 0 AS distance
			FROM q JOIN heads h ON h.content_hash = q.content_hash
			UNION ALL
			SELECT q.i, h.id, 'text', bit_count((h.text_simhash # q.text_simhash)::bit(64))::int
			FROM q JOIN heads h ON bit_count((h.text_simhash # q.text_simhash)::bit(64)) <= $4
			UNION ALL
			SELECT q.i, h.id, 'avatar', bit_count((h.avatar_phash # q.image_phash)::bit(64))::int
			FROM q JOIN heads h ON bit_count((h.avatar_phash # q.image_phash)::bit(64)) <= $4
		) m
		ORDER BY i, id, distance, reason <> 'content'`, content, text, image, maxDistance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i int
		var m HashMatch
		if err := rows.Scan(&i, &m.ID, &m.Reason, &m.Distance); err != nil {
			return nil, err
		}
		// WITH ORDINALITY counts from 1.
		out[i-1] = append(out[i-1], m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, ms := range out {
		slices.SortStableFunc(ms, func(a, b HashMatch) int { return cmp.Compare(a.Distance, b.Distance) })
	}
	return out, nil
}
