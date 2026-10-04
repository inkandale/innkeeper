package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

type Collection struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	// Cards is how many cards it has. Only Collections sets it.
	Cards int `json:"cards"`
}

// collectionErr maps constraint violations on collection.
func collectionErr(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return ErrNameTaken
	case errors.As(err, &pgErr) && pgErr.Code == "23514":
		return fmt.Errorf("%w: collection name may not be empty", ErrInvalid)
	}
	return mapErr(err)
}

// CreateCollection adds an empty collection.
func (l *Library) CreateCollection(ctx context.Context, name, description string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `INSERT INTO collection (name, description) VALUES ($1, $2)
		RETURNING id, name, description, created_at`, name, description).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return c, collectionErr(err)
}

// UpdateCollection renames a collection or changes its description. Nil
// leaves a field as it is.
func (l *Library) UpdateCollection(ctx context.Context, id int64, name, description *string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `UPDATE collection SET name = coalesce($2, name), description = coalesce($3, description)
		WHERE id = $1 RETURNING id, name, description, created_at`, id, name, description).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return c, collectionErr(err)
}

// DeleteCollection removes a collection. Its cards stay in the library.
func (l *Library) DeleteCollection(ctx context.Context, id int64) error {
	tag, err := l.pool.Exec(ctx, `DELETE FROM collection WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Collections returns every collection with its card count, by name.
func (l *Library) Collections(ctx context.Context) ([]Collection, error) {
	rows, err := l.pool.Query(ctx, `SELECT c.id, c.name, c.description, c.created_at, count(cc.card_id)::int
		FROM collection c LEFT JOIN collection_card cc ON cc.collection_id = c.id
		GROUP BY c.id ORDER BY lower(c.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Collection])
}

// TagCount is a tag and how many cards have it.
type TagCount struct {
	Name  string `json:"name"`
	Cards int    `json:"cards"`
}

// Tags returns every tag with its card count, alphabetically.
func (l *Library) Tags(ctx context.Context) ([]TagCount, error) {
	rows, err := l.pool.Query(ctx, `SELECT t.name, count(ct.card_id)::int
		FROM tag t LEFT JOIN card_tag ct ON ct.tag_id = t.id
		GROUP BY t.id ORDER BY lower(t.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[TagCount])
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
