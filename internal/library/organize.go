package library

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SetCardTags replaces a card's tags. Tag names are trimmed and matched
// case-insensitively; new names create tags.
func (l *Library) SetCardTags(ctx context.Context, cardID int64, names []string) error {
	clean := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			clean = append(clean, n)
		}
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO tag (name)
		SELECT DISTINCT ON (lower(n)) n FROM unnest($1::text[]) n
		ON CONFLICT (lower(name)) DO NOTHING`, clean); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM card_tag WHERE card_id = $1`, cardID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_tag (card_id, tag_id)
		SELECT $1, id FROM tag WHERE lower(name) IN (SELECT lower(n) FROM unnest($2::text[]) n)`,
		cardID, clean); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

// CardTags returns a card's tag names in alphabetical order.
func (l *Library) CardTags(ctx context.Context, cardID int64) ([]string, error) {
	rows, err := l.pool.Query(ctx, `SELECT t.name FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
		WHERE ct.card_id = $1 ORDER BY lower(t.name)`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Collection is a named group of cards. A card can be in many.
type Collection struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// CreateCollection adds an empty collection.
func (l *Library) CreateCollection(ctx context.Context, name, description string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `INSERT INTO collection (name, description) VALUES ($1, $2)
		RETURNING id, name, description, created_at`, name, description).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return c, err
}

// AddToCollection adds a card to a collection. Adding it twice is a no-op.
func (l *Library) AddToCollection(ctx context.Context, collectionID, cardID int64) error {
	_, err := l.pool.Exec(ctx, `INSERT INTO collection_card (collection_id, card_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, collectionID, cardID)
	return mapErr(err)
}

// RemoveFromCollection removes a card from a collection.
func (l *Library) RemoveFromCollection(ctx context.Context, collectionID, cardID int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM collection_card WHERE collection_id = $1 AND card_id = $2`,
		collectionID, cardID)
	return err
}

// CollectionCards returns the IDs of a collection's cards, oldest addition first.
func (l *Library) CollectionCards(ctx context.Context, collectionID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT card_id FROM collection_card WHERE collection_id = $1
		ORDER BY added_at, card_id`, collectionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// LinkLorebook links a card to a lorebook's head revision. Linking twice is a no-op.
func (l *Library) LinkLorebook(ctx context.Context, cardID, lorebookID int64) error {
	_, err := l.pool.Exec(ctx, `INSERT INTO card_lorebook (card_id, lorebook_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, cardID, lorebookID)
	return mapErr(err)
}

// UnlinkLorebook removes a link between a card and a lorebook.
func (l *Library) UnlinkLorebook(ctx context.Context, cardID, lorebookID int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM card_lorebook WHERE card_id = $1 AND lorebook_id = $2`,
		cardID, lorebookID)
	return err
}

// CardLorebooks returns the IDs of the lorebooks linked to a card.
func (l *Library) CardLorebooks(ctx context.Context, cardID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT lorebook_id FROM card_lorebook WHERE card_id = $1
		ORDER BY lorebook_id`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
