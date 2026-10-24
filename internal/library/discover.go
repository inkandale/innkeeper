package library

import (
	"cmp"
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// DiscoveredCard is a card found from another: similar or related to it.
type DiscoveredCard struct {
	CardID    int64   `json:"card_id"`
	LibraryID int64   `json:"library_id"`
	Name      string  `json:"name"`
	Avatar    string  `json:"avatar,omitempty" doc:"The head revision's avatar blob, if it has one."`
	Score     float64 `json:"score,omitempty" doc:"How alike, 0 to 1; similar cards only."`
}

// SimilarPage is one page of similar cards.
type SimilarPage struct {
	Basis      string           `json:"basis" enum:"embedding,tags" doc:"embedding ranks by meaning; tags, by shared tags, when there is no embed instance or the card has no vector yet."`
	Items      []DiscoveredCard `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

// SimilarOptions narrows SimilarCards.
type SimilarOptions struct {
	// Instance is the embed instance, or 0 to rank by tags.
	Instance     int64
	AllLibraries bool
	Page         Page
}

// discoverPageSize is the default page of similar and related cards.
const discoverPageSize = 20

// minSharedTags is how many tags a card must share to count as similar by tags.
const minSharedTags = 2

// window turns p into a limit and offset. Discovery cursors are offsets: an
// exact scan ranks every candidate, so a keyset saves nothing.
func (p Page) window() (limit, offset int, err error) {
	limit = cmp.Or(p.Size, discoverPageSize)
	switch {
	case p.Cursor != nil && p.Number != 0:
		return 0, 0, fmt.Errorf("%w: page takes a cursor or a number, not both", ErrInvalid)
	case p.Cursor != nil:
		offset, err = strconv.Atoi(*p.Cursor)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("%w: bad cursor %q", ErrInvalid, *p.Cursor)
		}
	case p.Number > 0:
		offset = (p.Number - 1) * limit
	}
	return limit, offset, nil
}

// discoverWith is the WITH clause discovery queries share: fam holds every
// card in $1's variant family, and self is $1's head, materialized so its
// tags are computed once rather than per candidate.
const discoverWith = `WITH RECURSIVE up AS (
		SELECT id, variant_of_id FROM card WHERE id = $1
		UNION SELECT p.id, p.variant_of_id FROM card p JOIN up ON p.id = up.variant_of_id
	), fam AS (
		SELECT id FROM up WHERE variant_of_id IS NULL
		UNION SELECT x.id FROM card x JOIN fam ON x.variant_of_id = fam.id
	), self AS MATERIALIZED (
		SELECT c.library_id, r.content_hash, tag_keys(r.tags) AS tags, lower(btrim(r.data->>'creator')) AS creator
		FROM card c JOIN card_revision r ON r.id = c.head_revision_id WHERE c.id = $1
	)`

// discoverWhere leaves out the card, its family, and archived cards.
const discoverWhere = `c.archived_at IS NULL AND c.id NOT IN (SELECT id FROM fam)`

// SimilarCards returns cards like cardID, closest first: by embedding
// distance when o.Instance is set and the card has a vector from it, else
// by shared tags. Exact copies and the card's variants are left out; other
// panels show them.
func (l *Library) SimilarCards(ctx context.Context, cardID int64, o SimilarOptions) (SimilarPage, error) {
	limit, offset, err := o.Page.window()
	if err != nil {
		return SimilarPage{}, err
	}
	basis, err := l.similarBasis(ctx, cardID, o.Instance)
	if err != nil {
		return SimilarPage{}, err
	}
	// ponytail: exact scan over every vector; add an HNSW index when an archive makes this slow.
	sql := discoverWith + `, q AS (SELECT embedding FROM card_embedding WHERE instance_id = $2 AND card_id = $1)
		SELECT c.id, c.library_id, coalesce(r.data->>'name', ''), coalesce(r.avatar_blob, ''), greatest(0, 1 - (e.embedding <=> q.embedding))
		FROM self CROSS JOIN q
		JOIN card_embedding e ON e.instance_id = $2 AND vector_dims(e.embedding) = vector_dims(q.embedding)
		JOIN card c ON c.id = e.card_id
		JOIN card_revision r ON r.id = c.head_revision_id
		WHERE ` + discoverWhere + ` AND r.content_hash <> self.content_hash AND ($3 OR c.library_id = self.library_id)
		ORDER BY e.embedding <=> q.embedding, c.id
		LIMIT $4 OFFSET $5`
	if basis == "tags" {
		sql = discoverWith + `
		SELECT c.id, c.library_id, coalesce(r.data->>'name', ''), coalesce(r.avatar_blob, ''), x.shared::float8 / (cardinality(self.tags) + x.n - x.shared)
		FROM self
		JOIN card_revision r ON tag_keys(r.tags) && self.tags
		JOIN card c ON c.head_revision_id = r.id
		CROSS JOIN LATERAL (SELECT count(*) FILTER (WHERE t = ANY(self.tags)) AS shared, count(*) AS n
			FROM unnest(tag_keys(r.tags)) t) x
		WHERE ` + discoverWhere + ` AND r.content_hash <> self.content_hash AND ($3 OR c.library_id = self.library_id)
			AND x.shared >= ` + strconv.Itoa(minSharedTags) + ` AND $2::bigint IS NOT NULL
		ORDER BY 5 DESC, c.id
		LIMIT $4 OFFSET $5`
	}
	items, next, err := l.discoverPage(ctx, sql, limit, offset, cardID, o.Instance, o.AllLibraries, limit+1, offset)
	if err != nil {
		return SimilarPage{}, fmt.Errorf("similar cards to %d: %w", cardID, err)
	}
	return SimilarPage{Basis: basis, Items: items, NextCursor: next}, nil
}

// similarBasis is embedding when instance is set and has a vector for cardID, else tags.
func (l *Library) similarBasis(ctx context.Context, cardID, instance int64) (string, error) {
	if instance == 0 {
		return "tags", nil
	}
	var has bool
	err := l.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM card_embedding WHERE instance_id = $1 AND card_id = $2)`,
		instance, cardID).Scan(&has)
	switch {
	case err != nil:
		return "", err
	case !has:
		return "tags", nil
	}
	return "embedding", nil
}

// discoverPage runs a discovery query that selects (id, library_id, name,
// avatar, score), asked for limit+1 rows, and returns limit of them and the next
// cursor when there are more.
func (l *Library) discoverPage(ctx context.Context, sql string, limit, offset int, args ...any) ([]DiscoveredCard, *string, error) {
	rows, err := l.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[DiscoveredCard])
	if err != nil {
		return nil, nil, err
	}
	if items == nil {
		items = []DiscoveredCard{}
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	next := strconv.Itoa(offset + limit)
	return items[:limit], &next, nil
}

// RelatedGroups are the ways a card links to others, in display order.
var RelatedGroups = []string{"creator", "lorebook", "collection"}

// relatedWhere is each group's condition on c, a candidate, against self
// and $1. Collections are per-library, so that group never leaves the card's.
var relatedWhere = map[string]string{
	"creator": `self.creator <> '' AND lower(btrim(r.data->>'creator')) = self.creator`,
	"lorebook": `c.id IN (SELECT o.card_id FROM card_lorebook o
		JOIN card_lorebook mine ON mine.lorebook_id = o.lorebook_id WHERE mine.card_id = $1)`,
	"collection": `c.id IN (SELECT o.card_id FROM collection_card o
		JOIN collection_card mine ON mine.collection_id = o.collection_id WHERE mine.card_id = $1)`,
}

// RelatedPage is one page of one group of related cards.
type RelatedPage struct {
	Group      string           `json:"group" enum:"creator,lorebook,collection"`
	Items      []DiscoveredCard `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

// RelatedCards returns one page of the cards linked to cardID by group,
// lowest ID first, leaving out its variants and archived cards.
func (l *Library) RelatedCards(ctx context.Context, cardID int64, group string, p Page) (RelatedPage, error) {
	cond, ok := relatedWhere[group]
	if !ok {
		return RelatedPage{}, fmt.Errorf("%w: group must be one of %v", ErrInvalid, RelatedGroups)
	}
	limit, offset, err := p.window()
	if err != nil {
		return RelatedPage{}, err
	}
	// ponytail: the creator group scans every head; add an expression index if it shows up slow.
	sql := discoverWith + `
		SELECT c.id, c.library_id, coalesce(r.data->>'name', ''), coalesce(r.avatar_blob, ''), 0::float8
		FROM self CROSS JOIN card c
		JOIN card_revision r ON r.id = c.head_revision_id
		WHERE ` + discoverWhere + ` AND ` + cond + `
		ORDER BY c.id
		LIMIT $2 OFFSET $3`
	items, next, err := l.discoverPage(ctx, sql, limit, offset, cardID, limit+1, offset)
	if err != nil {
		return RelatedPage{}, fmt.Errorf("%s related to card %d: %w", group, cardID, err)
	}
	return RelatedPage{Group: group, Items: items, NextCursor: next}, nil
}
