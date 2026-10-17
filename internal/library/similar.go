package library

import (
	"cmp"
	"context"
	"fmt"
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

// SimilarCards returns the other cards in cardID's library whose head
// revision is like cardID's, within maxDistance bits, closest first.
func (l *Library) SimilarCards(ctx context.Context, cardID int64, maxDistance int) ([]Match, error) {
	if maxDistance > NearDistance {
		return nil, errWideDistance
	}
	rows, err := l.pool.Query(ctx, `
		WITH q AS (
			SELECT r.content_hash, r.text_simhash, b.avatar_phash AS image_phash, c.library_id, c.id AS self
			FROM card c
			JOIN card_revision r ON r.id = c.head_revision_id
			LEFT JOIN blob b ON b.sha256 = r.avatar_blob
			WHERE c.id = $2
		)
		SELECT m.* FROM q CROSS JOIN LATERAL (`+nearSQL(Card)+`) m
		ORDER BY distance, id, reason`, maxDistance, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Match])
}

// nearSQL selects (id, reason, distance) for one probe row q (content_hash,
// text_simhash, image_phash, library_id, self): the entities of k in q's
// library, except self, whose head revision has the same content_hash or a
// hash within $1 bits. Candidates come from shared hash bands, which find
// every hash within NearDistance bits. Run it in a LATERAL join so each
// probe drives its own index lookups.
func nearSQL(k *Kind) string {
	head := ` JOIN ` + k.Table + ` e ON e.id = r.` + k.FK + ` AND e.head_revision_id = r.id
		WHERE e.library_id = q.library_id AND e.id <> q.self AND `
	return `
		SELECT e.id, 'content' AS reason, 0 AS distance
		FROM ` + k.RevTable + ` r` + head + `r.content_hash = q.content_hash
		UNION ALL
		SELECT e.id, 'text', bit_count((r.text_simhash # q.text_simhash)::bit(64))::int
		FROM ` + k.RevTable + ` r` + head + `r.text_bands && hash_bands(q.text_simhash)
			AND bit_count((r.text_simhash # q.text_simhash)::bit(64)) <= $1
		UNION ALL
		SELECT e.id, 'avatar', bit_count((b.avatar_phash # q.image_phash)::bit(64))::int
		FROM blob b JOIN ` + k.RevTable + ` r ON r.avatar_blob = b.sha256` + head + `b.phash_bands && hash_bands(q.image_phash)
			AND bit_count((b.avatar_phash # q.image_phash)::bit(64)) <= $1`
}

var errWideDistance = fmt.Errorf("%w: similar lookup finds at most %d differing bits", ErrInvalid, NearDistance)

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

// MatchHashes returns, for each of hashes, the entities in library whose
// head revision has the same content_hash, or a text_simhash or avatar phash
// within maxDistance bits, closest first. An entity that matches more than
// one way is listed once, by its closest reason.
func (s *Entities) MatchHashes(ctx context.Context, library int64, hashes []Hashes, maxDistance int) ([][]HashMatch, error) {
	if maxDistance > NearDistance {
		return nil, errWideDistance
	}
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
			SELECT q.*, $5::bigint AS library_id, 0 AS self
			FROM unnest($2::text[], $3::bigint[], $4::bigint[]) WITH ORDINALITY AS q (content_hash, text_simhash, image_phash, i)
		)
		SELECT DISTINCT ON (i, id) i::int, id, reason, distance FROM q CROSS JOIN LATERAL (`+nearSQL(s.k)+`) m
		ORDER BY i, id, distance, reason <> 'content'`, maxDistance, content, text, image, library)
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
